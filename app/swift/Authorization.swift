import AppKit
import Foundation
import Network

/// Exists only for a user-initiated login. Never stores or logs callback parameters.
@MainActor final class AuthorizationRelay {
    private var listener: NWListener?
    private var connections: [NWConnection] = []
    private var forwarding = false
    private let path: String
    private let port: UInt16
    private let forward: (String) async throws -> Void
    init(callback: URL, forward: @escaping (String) async throws -> Void) throws {
        guard callback.scheme == "http", ["localhost", "127.0.0.1"].contains(callback.host ?? ""),
              ["/auth/callback", "/callback"].contains(callback.path), let port = callback.port,
              let value = UInt16(exactly: port), value > 0 else { throw URLError(.badURL) }
        self.path = callback.path; self.port = value; self.forward = forward
    }

    func start() async throws {
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: NWEndpoint.Port(rawValue: port)!)
        let listener = try NWListener(using: parameters)
        self.listener = listener
        listener.newConnectionHandler = { [weak self] connection in
            Task { @MainActor in
                guard let self, self.listener != nil else { connection.cancel(); return }
                guard self.connections.count < 8 else { connection.cancel(); return }
                self.connections.append(connection); connection.start(queue: .main)
                DispatchQueue.main.asyncAfter(deadline: .now() + 30) { connection.cancel() }
                self.receive(connection, buffer: Data())
            }
        }
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            var settled = false
            listener.stateUpdateHandler = { state in
                guard !settled else { return }
                switch state {
                case .ready: settled = true; continuation.resume()
                case let .failed(error): settled = true; continuation.resume(throwing: error)
                case .cancelled: settled = true; continuation.resume(throwing: CancellationError())
                default: break
                }
            }
            listener.start(queue: .main)
        }
    }

    private func receive(_ connection: NWConnection, buffer: Data) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 16385) { [weak self] data, _, complete, error in
            Task { @MainActor in
                guard let self, self.listener != nil else { connection.cancel(); return }
                var buffer = buffer; if let data {
                    buffer.append(data)
                }
                guard buffer.count <= 16384, error == nil else { self.respond(connection, success: false); return }
                guard let text = String(data: buffer, encoding: .utf8), text.contains("\r\n\r\n") else {
                    if complete {
                        self.respond(connection, success: false)
                    } else {
                        self.receive(connection, buffer: buffer)
                    }
                    return
                }
                let words = text.components(separatedBy: "\r\n")[0].split(separator: " ")
                guard words.count == 3, words[0] == "GET", words[2] == "HTTP/1.1",
                      words[1].hasPrefix(self.path + "?"),
                      let parts = URLComponents(string: "http://127.0.0.1" + words[1]),
                      parts.path == self.path, parts.fragment == nil, let query = parts.percentEncodedQuery
                else {
                    self.respond(connection, success: false); return
                }
                guard !self.forwarding else { self.respond(connection, success: false); return }
                self.forwarding = true
                do {
                    try await self.forward(query)
                    // The callback is single-use. Keep the response connection alive
                    // long enough to send its result, but accept no further callbacks.
                    self.listener?.cancel(); self.listener = nil
                    self.respond(connection, success: true)
                } catch {
                    self.forwarding = false; self.respond(connection, success: false)
                }
            }
        }
    }

    private func respond(_ connection: NWConnection, success: Bool) {
        let body = "<!doctype html><meta charset=utf-8><title>Pot</title><p>" +
            (success ? "Authorization received. Return to Pot to check the result." :
                "This authorization could not be completed. Return to Pot and retry.") + "</p>"
        let response = "HTTP/1.1 \(success ? "200 OK" : "400 Bad Request")\r\nContent-Type: text/html; charset=utf-8\r\nCache-Control: no-store\r\nContent-Security-Policy: default-src 'none'\r\nConnection: close\r\nContent-Length: \(body.utf8.count)\r\n\r\n\(body)"
        connection.send(content: Data(response.utf8), completion: .contentProcessed { _ in connection.cancel() })
        connections.removeAll { $0 === connection }
    }

    func stop() {
        listener?.cancel(); listener = nil
        for connection in connections {
            connection.cancel()
        }; connections.removeAll()
    }
}

extension SettingsPanel {
    func startAuthorization(_ engine: String) {
        guard !loginVisible, let runtime = bridge?.runtime else { return }
        loginEngine = engine; loginVisible = true; loginMessage = "正在打开官方授权…"; result = ""
        var generation = UUID().uuidString
        authorizationID = generation
        authorizationTask = Task { [weak self] in
            guard let self else { return }
            var attempt = generation
            do {
                var response = try await runtime.request(
                    "api/v1/authorization/start",
                    body: ["engine": engine, "attempt_id": generation]
                )
                attempt = response["attempt_id"] as? String ?? ""
                guard self.authorizationID == generation, !Task.isCancelled else {
                    _ = try? await runtime.request("api/v1/authorization/cancel", body: ["attempt_id": attempt]); return
                }
                if let raw = response["callback_url"] as? String, let callback = URL(string: raw) {
                    do {
                        let relay = try AuthorizationRelay(callback: callback) { query in
                            _ = try await runtime.request(
                                "api/v1/authorization/callback",
                                body: ["attempt_id": attempt, "query": query]
                            )
                        }
                        self.authorizationRelay = relay; try await relay.start()
                    } catch {
                        self.authorizationRelay?.stop(); self.authorizationRelay = nil
                        _ = try? await runtime.request("api/v1/authorization/cancel", body: ["attempt_id": attempt])
                        generation = UUID().uuidString; self.authorizationID = generation; attempt = generation
                        response = try await runtime.request(
                            "api/v1/authorization/start",
                            body: ["engine": engine, "device": true, "attempt_id": generation]
                        )
                        attempt = response["attempt_id"] as? String ?? ""
                    }
                }
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
                    self.authorizationRelay?.stop(); self.authorizationRelay = nil
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
                    self.authorizationRelay?.stop(); self.authorizationRelay = nil
                    self.loginVisible = false; self.authorizationID = ""; self.authorizationURL = ""
                    self.loginMessage = "授权无法完成，请确认环境已启动并重试。"; self.result = self.loginMessage
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
        authorizationRelay?.stop(); authorizationRelay = nil
        loginVisible = false; loginMessage = "授权已取消。"
        result = loginMessage
        if !attempt.isEmpty, let runtime = bridge?.runtime {
            Task { _ = try? await runtime.request("api/v1/authorization/cancel", body: ["attempt_id": attempt]) }
        }
    }
}
