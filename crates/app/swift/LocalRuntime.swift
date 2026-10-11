import AppKit
import Foundation
import WebKit

/// Native desktop session projection; Sandbox Provider owns process lifecycle.
@MainActor final class LocalRuntime {
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
            Task {
                let client = RuntimeClient(); client.root = root; client.remoteMode = false; _ = try? await client
                    .request(
                        "api/v1/desktop/sessions/close",
                        body: ["session_id": session]
                    )
            }
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
            // The Sandbox Provider owns its Helper and starts it only for explicit lifecycle work.
            timer = Timer
                .scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
                    MainActor.assumeIsolated { self?.poll() }
                }
        } catch { label.stringValue = "Helper failed: \(error)" }
    }

    func send(_ method: String, completion: (([String: Any]) -> Void)? = nil) {
        let target = sandboxID
        let generation = viewGeneration
        if ["start", "stop", "desktop"].contains(method) {
            Task {
                let client = RuntimeClient(); client.root = root
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
        completion?(["error": "Unsupported desktop lifecycle action"])
    }

    func poll() {
        if polling {
            return
        }; polling = true
        let target = sandboxID
        let generation = viewGeneration
        Task {
            defer { polling = false }
            let client = RuntimeClient(); client.root = root
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
            Task {
                defer { probing = false }
                let client = RuntimeClient(); client.root = root
                do {
                    let result = try await client.request("api/v1/desktop/sessions", body: ["sandbox_id": target])
                    guard let session = result["session"] as? [String: Any], let sessionID = session["id"] as? String,
                          let path = result["path"] as? String,
                          let data = try? Data(contentsOf: root.appendingPathComponent("product/runtime.json")),
                          let endpoint = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                          let port = endpoint["port"] as? Int else { return }
                    guard target == sandboxID, generation == viewGeneration else {
                        _ = try? await client.request(
                            "api/v1/desktop/sessions/close",
                            body: ["session_id": sessionID]
                        ); return
                    }
                    desktopSession = sessionID
                    let address = "http://127.0.0.1:\(port)\(path)?native=1"
                    guard let url = URL(string: address) else { return }
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

    func cleanup() {
        closeDesktop()
        timer?.invalidate(); timer = nil
    }
}
