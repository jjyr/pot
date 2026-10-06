import AppKit
import Foundation

extension SettingsPanel {
    func startAuthorization(_ engine: String) {
        guard !loginVisible, let runtime = bridge?.runtime else { return }
        loginEngine = engine; loginVisible = true; loginMessage = "正在打开官方授权…"; result = ""
        let generation = UUID().uuidString
        authorizationID = generation
        authorizationTask = Task { [weak self] in
            guard let self else { return }
            var attempt = generation
            do {
                let response = try await runtime.request(
                    "api/v1/authorization/start",
                    body: ["engine": engine, "attempt_id": generation]
                )
                attempt = response["attempt_id"] as? String ?? ""
                guard self.authorizationID == generation, !Task.isCancelled else {
                    _ = try? await runtime.request("api/v1/authorization/cancel", body: ["attempt_id": attempt]); return
                }
                self.authorizationID = attempt
                self.authorizationURL = response["authorization_url"] as? String ?? ""
                let code = response["user_code"] as? String ?? ""
                self.loginMessage = code.isEmpty ? "请在浏览器完成官方授权，然后返回 Pot。" : "请在官方页面输入验证码：\(code)，然后返回 Pot。"
                if !self.authorizationURL.isEmpty {
                    guard let url = URL(string: self.authorizationURL),
                          NSWorkspace.shared.open(url) else { throw URLError(.badURL) }
                }
                while !Task.isCancelled, self.authorizationID == attempt {
                    try await Task.sleep(nanoseconds: 750_000_000)
                    let state = try await runtime.request("api/v1/authorization/status?attempt_id=\(attempt)")
                    let phase = state["phase"] as? String ?? "failed"
                    if phase == "waiting" {
                        continue
                    }

                    self.loginVisible = false; self.authorizationURL = ""; self.authorizationID = ""
                    if phase == "authenticated" {
                        self.modelOptions = state["models"] as? [String] ?? []
                        if self.connectionModel.isEmpty, let first = self.modelOptions.first {
                            self.connectionModel = first; self.draftRevision += 1
                        }
                        self.loginMessage = "授权成功，选择模型后即可保存。"; self.result = self.loginMessage
                        self.refreshAgents()
                        self.bridge?.showWindow()
                    } else {
                        self
                            .loginMessage = phase == "subscription_required" ? "登录成功，但当前账号尚无使用权限。请确认官方套餐后重试。" : phase ==
                            "expired" ? "授权已超时，请重新登录。" : "授权未完成，请重试。"; self.result = self
                            .loginMessage
                    }
                    return
                }
            } catch {
                if self.authorizationID == generation || self.authorizationID == attempt {
                    self.loginVisible = false; self.authorizationID = ""; self.authorizationURL = ""
                    self.loginMessage = "授权无法完成，请检查本地 Codex 与网络后重试。"; self.result = self.loginMessage
                }
                if !attempt.isEmpty {
                    _ = try? await runtime.request(
                        "api/v1/authorization/cancel",
                        body: ["attempt_id": attempt]
                    )
                }
            }
        }
    }

    func stopAuthorization() {
        let attempt = authorizationID; authorizationID = ""; authorizationURL = ""
        authorizationTask?.cancel(); authorizationTask = nil
        loginVisible = false; loginMessage = "授权已取消。"
        result = loginMessage
        if !attempt.isEmpty, let runtime = bridge?.runtime {
            Task { _ = try? await runtime.request("api/v1/authorization/cancel", body: ["attempt_id": attempt]) }
        }
    }
}
