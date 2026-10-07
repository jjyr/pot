import AppKit
import Darwin
import Foundation
import WebKit

/// App-owned user process; bundle-relative executable/resources, private Unix IPC.
@MainActor final class LocalRuntime {
    let process = Process()
    var client = RuntimeClient()
    var selections: [String: String] = [:]
    let root: URL
    weak var web: WKWebView?
    let label = NSTextField(wrappingLabelWithString: L.text("native.runtime-starting"))
    var timer: Timer?
    var polling = false
    var probing = false
    var loaded = ""
    var displayReady = false
    var sandboxID = "default"
    var desktopSupported = true
    var viewGeneration = 0
    var desktopSession = ""
    var requestedDesktops = Set<String>()

    func closeDesktop() {
        let session = desktopSession; desktopSession = ""
        if !session.isEmpty {
            let client = client
            Task { _ = try? await client.request(
                "api/v1/desktop/sessions/close",
                body: ["session_id": session]
            ) }
        }
    }

    func selectSandbox(_ id: String, desktop: Bool = true) {
        closeDesktop()
        desktopSupported = desktop
        viewGeneration += 1; sandboxID = id; loaded = ""; displayReady = false; web?.stopLoading(); web?.isHidden = true
        poll()
    }

    var onState: (([String: Any]) -> Void)?
    init(view _: NSView, web: WKWebView) {
        self.web = web
        root = URL(fileURLWithPath: ProcessInfo.processInfo
            .environment["POT_LOCAL_ROOT"] ?? NSHomeDirectory() + "/Library/Application Support/pot")
        do {
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
            label.frame = NSRect(x: 16, y: 120, width: 300, height: 110); label
                .textColor = .white // Native chat presents status
            let start = NSButton(title: "Start local desktop", target: self, action: #selector(startVM)); start
                .bezelStyle = .rounded; start.appearance = NSAppearance(named: .darkAqua); start.frame = NSRect(
                    x: 16,
                    y: 70,
                    width: 160,
                    height: 32
                ) // Start is automatic
            let stop = NSButton(title: "Stop", target: self, action: #selector(stopVM)); stop
                .bezelStyle = .rounded; stop.appearance = NSAppearance(named: .darkAqua); stop.frame = NSRect(
                    x: 185,
                    y: 70,
                    width: 100,
                    height: 32
                ) // Stop via Quit
            process.executableURL = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/PotHelper")
            process.arguments = [
                root.path,
                Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/runtime").path,
                "--sandbox-only",
            ]
            let log = root.appendingPathComponent("helper.log"); FileManager.default.createFile(
                atPath: log.path,
                contents: nil
            )
            let handle = try FileHandle(forWritingTo: log); process.standardOutput = handle; process
                .standardError = handle
            try process.run()
            timer = Timer
                .scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
                    MainActor.assumeIsolated { self?.poll() }
                }
        } catch { label.stringValue = "Helper failed: \(error)" }
    }

    nonisolated static func request(path: String, method: String, sandboxID: String = "default") -> [String: Any] {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0); defer { close(fd) }
        var timeout = timeval(tv_sec: 15, tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        var addr = sockaddr_un(); addr.sun_family = sa_family_t(AF_UNIX)
        guard path.utf8.count < 104 else { return ["error": "Socket path too long"] }
        withUnsafeMutableBytes(of: &addr.sun_path) { dest in
            path.utf8CString.withUnsafeBytes { dest.copyBytes(from: $0) }
        }
        let result = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(
            fd,
            $0,
            socklen_t(MemoryLayout<sockaddr_un>.size)
        ) }}
        guard result == 0 else { return ["error": "Helper IPC unavailable"] }
        var data = try! JSONSerialization.data(withJSONObject: [
            "id": UUID().uuidString,
            "method": method,
            "sandbox_id": sandboxID,
        ]); data
            .append(10)
        data.withUnsafeBytes { _ = Darwin.write(fd, $0.baseAddress, data.count) }
        var response = Data(); var byte: UInt8 = 0
        while response.count < 65536, read(fd, &byte, 1) == 1 {
            if byte == 10 {
                break
            }; response.append(byte)
        }
        return (try? JSONSerialization.jsonObject(with: response)) as? [String: Any] ??
            ["error": "Invalid helper response"]
    }

    func send(_ method: String, completion: (([String: Any]) -> Void)? = nil) {
        let target = sandboxID
        let generation = viewGeneration
        if ["start", "stop", "desktop"].contains(method) {
            let client = client
            Task {
                do {
                    let value = try await client.request(
                        "api/v1/sandboxes",
                        body: ["action": method, "sandbox_id": target]
                    )
                    let response: [String: Any] = ["result": value["state"] ?? value]
                    if target == sandboxID, generation == viewGeneration {
                        if method == "desktop" {
                            requestedDesktops.insert(target)
                        }
                        render(response)
                    }; completion?(response)
                } catch { completion?(["error": error.localizedDescription]) }
            }
            return
        }
        let path = root.appendingPathComponent("helper.sock").path
        DispatchQueue.global().async {
            let response = Self.request(path: path, method: method, sandboxID: target)
            DispatchQueue.main
                .async {
                    if target == self.sandboxID,
                       generation == self.viewGeneration
                    {
                        self.render(response)
                    }; completion?(response)
                }
        }
    }

    func poll() {
        // A missing optional Helper must not prevent local chat or Server switching.
        // Process.terminationStatus raises an Objective-C exception before launch.
        if client.serverID == "local", process.processIdentifier != 0, !process.isRunning {
            label.stringValue = "Helper exited (\(process.terminationStatus)); reopen App to recover"
        }
        if polling {
            return
        }; polling = true
        let target = sandboxID
        let generation = viewGeneration
        let client = client
        Task {
            defer {
                if generation == viewGeneration {
                    polling = false
                }
            }
            if let inventory = try? await client.request("api/v1/sandboxes"),
               let items = inventory["items"] as? [[String: Any]],
               let item = items.first(where: { $0["id"] as? String == target }),
               target == sandboxID, generation == viewGeneration
            {
                render(["result": item["state"] ?? [:]])
            }
        }
    }

    func render(_ response: [String: Any]) {
        guard let state = response["result"] as? [String: Any]
        else { label.stringValue = String(describing: response["error"] ?? "IPC error"); return }
        let phase = state["phase"] as? String ?? "unknown"
        if ["stopped", "failed", "stopping", "unavailable"].contains(phase) {
            requestedDesktops.remove(sandboxID); closeDesktop(); loaded = ""; displayReady = false
        }
        onState?(state)
        label.stringValue = L.format(
            "native.runtime-status",
            [L.phase(phase), L.message(String(describing: state["error"] ?? ""))]
        )
        if phase == "running", desktopSupported, requestedDesktops.contains(sandboxID), loaded.isEmpty, !probing {
            probing = true
            let target = sandboxID
            let generation = viewGeneration
            let client = client
            Task {
                defer {
                    if generation == viewGeneration {
                        probing = false
                    }
                }
                do {
                    let result = try await client.request("api/v1/desktop/sessions", body: ["sandbox_id": target])
                    guard let session = result["session"] as? [String: Any], let sessionID = session["id"] as? String,
                          let path = result["path"] as? String else { return }
                    guard target == sandboxID, generation == viewGeneration else {
                        _ = try? await client.request(
                            "api/v1/desktop/sessions/close",
                            body: ["session_id": sessionID]
                        ); return
                    }
                    desktopSession = sessionID
                    let address: String
                    if client.serverID == "local" {
                        guard let data = try? Data(contentsOf: root.appendingPathComponent("product/runtime.json")),
                              let endpoint = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                              let port = endpoint["port"] as? Int else { return }
                        address = "http://127.0.0.1:\(port)\(path)?native=1"
                    } else {
                        let info = try await client.request("client/desktop", body: ["path": path])
                        guard let url = info["url"] as? String, let origin = info["origin"] as? String,
                              let host = URL(string: origin)?.host else { return }
                        address = url
                        for field in (info["cookie"] as? String ?? "").components(separatedBy: "; ") {
                            let pieces = field.split(separator: "=", maxSplits: 1).map(String.init)
                            if pieces.count == 2, let cookie = HTTPCookie(properties: [
                                .domain: host, .path: "/", .name: pieces[0], .value: pieces[1],
                                .secure: origin.hasPrefix("https:") ? "TRUE" : "FALSE",
                            ]) {
                                await web?.configuration.websiteDataStore.httpCookieStore.setCookie(cookie)
                            }
                        }
                    }
                    guard target == sandboxID, generation == viewGeneration, let url = URL(string: address) else {
                        _ = try? await client.request(
                            "api/v1/desktop/sessions/close",
                            body: ["session_id": sessionID]
                        ); return
                    }
                    loaded = address; displayReady = false; web?.isHidden = true
                    web?.load(URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData))
                } catch { label.stringValue = error.localizedDescription }
            }
        }
        if phase == "stopped" || phase == "failed" || phase == "stopping" {
            loaded = ""; onState?(state)
        }
    }

    @objc func startVM() {
        send("start")
    }

    @objc func stopVM() {
        send("stop")
    }

    func reconnectDesktop() {
        requestedDesktops.insert(sandboxID)
        closeDesktop(); loaded = ""; displayReady = false; viewGeneration += 1; web?.stopLoading(); web?
            .isHidden = true; poll()
    }

    func switchServer(_ runtime: RuntimeClient) {
        selections[client.serverID] = sandboxID
        closeDesktop()
        client = runtime
        viewGeneration += 1
        sandboxID = selections[runtime.serverID] ?? "default"
        loaded = ""; displayReady = false; polling = false; probing = false
        requestedDesktops = []; web?.stopLoading(); web?.isHidden = true
        poll()
    }

    func cleanup() {
        closeDesktop()
        timer?.invalidate(); timer = nil; if process.isRunning {
            process.terminate()
            process.waitUntilExit()
        }
    }
}
