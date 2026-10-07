import AppKit
import Foundation

extension SettingsPanel {
    func startAuthorization(_ engine: String) {
        guard !loginVisible else { return }
        let runtime = runtime
        loginEngine = engine; loginVisible = true; loginMessage = L
            .text("authorization.opening-official-authorization"); result = ""
        let generation = UUID().uuidString
        authorizationID = generation
        authorizationTask = Task { [weak self] in
            guard let self else { return }
            var attempt = generation
            do {
                let response = try await runtime.request(
                    "api/v1/authorization/start",
                    body: ["engine": engine, "attempt_id": generation, "device": runtime.serverID != "local"]
                )
                attempt = response["attempt_id"] as? String ?? ""
                guard self.authorizationID == generation, !Task.isCancelled else {
                    _ = try? await runtime.request("api/v1/authorization/cancel", body: ["attempt_id": attempt]); return
                }
                self.authorizationID = attempt
                self.authorizationURL = response["authorization_url"] as? String ?? ""
                let code = response["user_code"] as? String ?? ""
                self.loginMessage = code.isEmpty ? L
                    .text("authorization.complete-official-authorization-in-your-browser-then") : L.format(
                        "authorization.enter-code-0-on-the-official-page",
                        [String(describing: code)]
                    )
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
                        self.loginMessage = L.text("authorization.authorization-complete-choose-a-model-to-save"); self
                            .result = self.loginMessage
                        self.refreshAgents()
                        self.bridge?.showWindow()
                    } else {
                        self
                            .loginMessage = phase == "subscription_required" ? L
                            .text("authorization.signed-in-but-this-account-does-not") :
                            phase ==
                            "expired" ? L.text("authorization.authorization-timed-out-sign-in-again") : L
                            .text("authorization.authorization-incomplete-try-again"); self.result = self
                            .loginMessage
                    }
                    return
                }
            } catch {
                if self.authorizationID == generation || self.authorizationID == attempt {
                    self.loginVisible = false; self.authorizationID = ""; self.authorizationURL = ""
                    self.loginMessage = L.text("authorization.unable-to-authorize-check-local-codex-and"); self
                        .result = self.loginMessage
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
        loginVisible = false; loginMessage = L.text("authorization.authorization-cancelled")
        result = loginMessage
        if !attempt.isEmpty {
            let runtime = runtime
            Task { _ = try? await runtime.request("api/v1/authorization/cancel", body: ["attempt_id": attempt]) }
        }
    }
}
