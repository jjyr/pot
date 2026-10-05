import AppKit
import Foundation
import WebKit

/// Bundled renderer only. Authentication and networking never enter JavaScript.
@MainActor final class SandboxTerminal: NSObject, WKNavigationDelegate, WKScriptMessageHandler {
    let web: WKWebView
    private let asset: URL
    private var socket: URLSessionWebSocketTask?
    private var sessionID: String?
    private var bootID = ""
    private var root: URL?
    private var ip = ""
    private var ready = false
    private var connecting = false
    private var generation = 0
    private var visible = false
    private var inputQueue = Data()
    private var sendingInput = false
    private var cols = 80
    private var rows = 24

    override init() {
        asset = Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/terminal/index.html")
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        web = WKWebView(frame: .zero, configuration: configuration)
        super.init()
        web.navigationDelegate = self
        configuration.userContentController.add(self, name: "potTerminal")
        web.loadFileURL(asset, allowingReadAccessTo: asset.deletingLastPathComponent())
        web.isHidden = true
    }

    func show(_ value: Bool, root: URL?, ip: String) {
        let newlyVisible = value && !visible
        visible = value; self.root = root; self.ip = ip; web.isHidden = !value
        if value {
            connect(); if newlyVisible {
                web.window?.makeFirstResponder(web)
                web.evaluateJavaScript("window.potTerminal?.focus()", completionHandler: nil)
            }
        }
    }

    private func credentials() throws -> (URL, String, String) {
        guard let root, !ip.isEmpty else { throw URLError(.notConnectedToInternet) }
        let data = try Data(contentsOf: root.appendingPathComponent("workspace/product/runtime.json"))
        guard let endpoint = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let port = endpoint["port"] as? Int, let token = endpoint["token"] as? String,
              let boot = endpoint["boot_id"] as? String,
              let url = URL(string: "http://\(ip):\(port)") else { throw URLError(.badServerResponse) }
        return (url, token, boot)
    }

    func request(_ path: String, body: [String: Any]? = nil) async throws -> [String: Any] {
        let (base, token, _) = try credentials()
        var request = URLRequest(url: base.appendingPathComponent(path))
        request.httpMethod = body == nil ? "GET" : "POST"; request.timeoutInterval = 10
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let body {
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, response) = try await URLSession.shared.data(for: request)
        let value = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        guard let response = response as? HTTPURLResponse, response.statusCode == 200 else {
            throw NSError(
                domain: "PotRuntime",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: String((value["message"] as? String ?? "Runtime 请求失败")
                        .prefix(1024))]
            )
        }
        return value
    }

    func connect() {
        guard ready, visible, !connecting else { return }
        do {
            let (_, _, boot) = try credentials()
            if bootID != boot {
                generation += 1; socket?
                    .cancel(with: .goingAway, reason: nil); socket = nil; sessionID = nil
                if !bootID.isEmpty {
                    status("Sandbox 已重启，原终端已结束")
                }
                bootID = boot
            }
            guard socket == nil else { return }
        } catch { status("等待 Sandbox Runtime；就绪后点击 Terminal 重连"); return }
        connecting = true
        let current = generation
        Task {
            defer { connecting = false }
            do {
                if sessionID == nil {
                    let existing = try await request("api/v1/terminals")
                    guard current == generation else { return }
                    let sessions = existing["sessions"] as? [[String: Any]] ?? []
                    if let shell = sessions
                        .first(where: {
                            $0["exit_code"] is NSNull && $0["attached"] as? Bool == false
                        })
                    {
                        sessionID = shell["id"] as? String
                    }
                }
                if sessionID == nil {
                    let result = try await request("api/v1/terminals", body: ["cols": cols, "rows": rows])
                    guard current == generation else { return }
                    sessionID = result["id"] as? String
                }
                guard let sessionID else { throw URLError(.badServerResponse) }
                let (base, token, _) = try credentials()
                var parts = URLComponents(
                    url: base.appendingPathComponent("api/v1/terminals/\(sessionID)/socket"),
                    resolvingAgainstBaseURL: false
                )!
                parts.scheme = "ws"
                var request = URLRequest(url: parts.url!)
                request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                let connection = URLSession.shared.webSocketTask(with: request)
                socket = connection; connection.resume()
                web.evaluateJavaScript("window.potTerminal.reset()", completionHandler: nil)
                receive(connection, generation: current)
                sendSize()
            } catch { status("终端连接失败；点击 Terminal 重连") }
        }
    }

    private func receive(_ connection: URLSessionWebSocketTask, generation current: Int) {
        Task {
            do {
                let message = try await connection.receive()
                guard current == generation, socket === connection else { return }
                switch message {
                case let .data(data):
                    // Await rendering to keep native output buffering bounded.
                    _ = try? await web.evaluateJavaScript("window.potTerminal.output('\(data.base64EncodedString())')")
                case let .string(text):
                    if let data = text.data(using: .utf8),
                       let result = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                       let code = result["exit_code"] as? Int
                    {
                        status("进程已退出：\(code)；关闭后可新建终端")
                    }
                @unknown default: break
                }
                receive(connection, generation: current)
            } catch {
                guard current == generation, socket === connection else { return }
                socket = nil; status("连接已断开，PTY 保留；点击 Terminal 重连")
            }
        }
    }

    func close() {
        guard let sessionID else { return }
        Task {
            do {
                _ = try await request("api/v1/terminals/\(sessionID)/close", body: [:])
                generation += 1; socket?.cancel(with: .normalClosure, reason: nil); socket = nil; self.sessionID = nil
                status("终端已关闭")
            } catch { status("关闭未确认，请重试") }
        }
    }

    private func status(_ text: String) {
        guard let data = try? JSONSerialization.data(withJSONObject: [text]), let json = String(
            data: data,
            encoding: .utf8
        ) else { return }
        web.evaluateJavaScript("window.potTerminal?.status(\(json)[0])", completionHandler: nil)
    }

    private func enqueue(_ data: Data) {
        guard socket != nil else { return }
        guard inputQueue.count + data.count <= 65536 else { status("粘贴超过 64 KiB 输入缓冲，请分段粘贴"); return }
        inputQueue.append(data)
        guard !sendingInput else { return }
        sendingInput = true
        let current = generation
        Task {
            defer { sendingInput = false }
            while current == generation, let connection = socket, !inputQueue.isEmpty {
                let chunk = inputQueue.prefix(8192); inputQueue.removeFirst(chunk.count)
                do { try await connection.send(.data(Data(chunk))) }
                catch { inputQueue.removeAll(); return }
            }
            inputQueue.removeAll()
        }
    }

    private func sendSize() {
        guard let data = try? JSONSerialization.data(withJSONObject: ["cols": cols, "rows": rows]), let text = String(
            data: data,
            encoding: .utf8
        ) else { return }
        socket?.send(.string(text)) { _ in }
    }

    func userContentController(_: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame,
              message.frameInfo.request.url?.standardizedFileURL == asset.standardizedFileURL,
              let body = message.body as? [String: Any], let action = body["action"] as? String else { return }
        switch action {
        case "ready": ready = true; connect()
        case "input":
            if let text = body["data"] as? String, let data = text.data(using: .utf8) {
                enqueue(data)
            }
        case "resize":
            if let c = body["cols"] as? Int, let r = body["rows"] as? Int {
                cols = max(1, min(500, c)); rows = max(
                    1,
                    min(300, r)
                ); sendSize()
            }
        case "link":
            if let text = body["url"] as? String, let url = URL(string: text),
               ["http", "https"].contains(url.scheme?.lowercased() ?? "")
            {
                NSWorkspace.shared.open(url)
            }
        default: break
        }
    }

    func webView(
        _: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        decisionHandler(navigationAction.request.url?.standardizedFileURL == asset
            .standardizedFileURL ? .allow : .cancel)
    }
}
