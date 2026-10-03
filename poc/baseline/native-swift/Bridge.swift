import AppKit
import WebKit
import Foundation

private var bridge: PotBridge?
@MainActor final class PotBridge: NSObject, WKNavigationDelegate {
    let status: NSStatusItem
    let web = WKWebView(frame: .zero)
    let host: NSWindow
    var timer: Timer?
    var checks = [String: Any]()
    init(host: NSWindow) {
        self.host = host
        status = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        super.init()
        status.button?.title = "Pot POC"
        let menu = NSMenu()
        let show = NSMenuItem(title: "Open Pot POC", action: #selector(showWindow), keyEquivalent: "")
        show.target = self; menu.addItem(show)
        let quit = NSMenuItem(title: "Quit POC", action: #selector(quitApp), keyEquivalent: "")
        quit.target = self; menu.addItem(quit); status.menu = menu
        guard let view = host.contentView else { return }
        web.frame = NSRect(x: 350, y: 0, width: view.bounds.width - 350, height: view.bounds.height)
        web.autoresizingMask = [.width, .height]; web.navigationDelegate = self
        view.addSubview(web)
        web.load(URLRequest(url: URL(string: "http://127.0.0.1:17410/")!))
        checks["gpui_host_window_found"] = host.title == "Pot POC"
        checks["native_status_item_created"] = status.button != nil
        checks["webview_attached_to_gpui_contentview"] = web.superview === view
        timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.checkWeb() }
        }
    }
    @objc func showWindow() { host.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true) }
    @objc func quitApp() { NSApp.terminate(nil) }
    func checkWeb() {
        web.evaluateJavaScript("window.pocState || {}") { [weak self] result, error in
            guard let self, let state = result as? [String: Any] else { return }
            self.checks["web"] = state
            guard state["connected"] as? Bool == true else { self.save(); return }
            self.timer?.invalidate()
            self.web.evaluateJavaScript("window.pocSendKey(); true") { _, error in
                self.checks["js_send_key_call"] = error == nil
                DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                    self.web.takeSnapshot(with: nil) { image, error in
                        if let image, let tiff = image.tiffRepresentation,
                           let bitmap = NSBitmapImageRep(data: tiff), let png = bitmap.representation(using: .png, properties: [:]) {
                            try? png.write(to: URL(fileURLWithPath: "/tmp/pot-research-20261003/native-viewer.png"))
                            self.checks["snapshot_saved"] = true
                        }
                        self.checks["snapshot_error"] = error?.localizedDescription ?? ""
                        self.host.orderOut(nil)
                        self.checks["window_hidden"] = !self.host.isVisible
                        self.showWindow()
                        self.checks["tray_action_reopens_same_window"] = self.host.isVisible && self.host.title == "Pot POC"
                        self.save()
                        DispatchQueue.main.asyncAfter(deadline: .now()+1) { NSApp.terminate(nil) }
                    }
                }
            }
        }
    }
    func save() {
        if let d = try? JSONSerialization.data(withJSONObject: checks, options: [.prettyPrinted, .sortedKeys]) {
            try? d.write(to: URL(fileURLWithPath: "/tmp/pot-research-20261003/native-ui.json"))
        }
    }
}
@_cdecl("pot_bridge_install") public func potBridgeInstall() {
    DispatchQueue.main.asyncAfter(deadline: .now()+1) {
        guard let w = NSApplication.shared.windows.first(where: { $0.title == "Pot POC" }) else { return }
        bridge = PotBridge(host:w)
    }
    DispatchQueue.main.asyncAfter(deadline:.now()+25) { NSApp.terminate(nil) }
}
