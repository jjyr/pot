import AppKit
import WebKit
import Foundation

// One GPUI-owned NSApplication/NSWindow. No second run loop or replacement delegate.
// The bridge owns status/web/observers; callbacks capture weak self. C strings are
// borrowed for the duration of each call and copied before asynchronous use.
private var bridge: PotBridge?
private let runID = ProcessInfo.processInfo.environment["POT_RUN_ID"] ?? UUID().uuidString
private let formatter = ISO8601DateFormatter()
private func record(_ event: String, _ details: [String: Any] = [:]) {
    var row = details
    row["event"] = event; row["time"] = formatter.string(from: Date())
    row["run_id"] = runID; row["pid"] = ProcessInfo.processInfo.processIdentifier
    row["window_id"] = "main"; row["connection_id"] = "local-input"
    if let data = try? JSONSerialization.data(withJSONObject: row, options: [.sortedKeys]) {
        FileHandle.standardOutput.write(data); FileHandle.standardOutput.write(Data([10]))
    }
}
@MainActor final class HostView: NSView {
    weak var viewer: WKWebView?
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if let viewer, let responder = window?.firstResponder as? NSView,
           responder === viewer || responder.isDescendant(of: viewer) {
            return viewer.performKeyEquivalent(with: event)
        }
        return super.performKeyEquivalent(with: event)
    }
}
@MainActor final class PotBridge: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
    let host: NSWindow
    let gpuiView: NSView
    let gpuiResponder: NSView
    let status: NSStatusItem
    let web: WKWebView
    var observers: [NSObjectProtocol] = []
    var keyMonitor: Any?
    var termination: DispatchSourceSignal?
    init(host: NSWindow, html: String) {
        self.host = host
        self.gpuiView = host.contentView!
        func findGPUI(_ view: NSView) -> NSView? {
            if String(describing: type(of: view)) == "GPUIView" { return view }
            return view.subviews.compactMap(findGPUI).first
        }
        guard let responder = findGPUI(host.contentView!) else { fatalError("GPUIView missing for pinned GPUI 0.2.2") }
        self.gpuiResponder = responder
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        web = WKWebView(frame: .zero, configuration: config)
        status = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        super.init()
        NSApp.setActivationPolicy(.regular) // Dock + Cmd-Tab deliberately available for P1.
        host.isReleasedWhenClosed = false
        status.button?.title = "pot POC"
        let menu = NSMenu()
        for (title, action) in [("Open pot", #selector(showWindow)), ("Hide pot", #selector(hideWindow)), ("Quit pot POC", #selector(quitApp))] {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
            item.target = self; item.representedObject = "tray"; menu.addItem(item)
        }
        status.menu = menu
        let main = NSMenu()
        let appItem = NSMenuItem(); main.addItem(appItem)
        appItem.submenu = menu.copy() as? NSMenu
        appItem.submenu?.items.forEach { $0.representedObject = "app-menu" }
        let editItem = NSMenuItem(title: "Edit", action: nil, keyEquivalent: "")
        let edit = NSMenu(title: "Edit")
        for (title, selector, key) in [("Cut", "cut:", "x"), ("Copy", "copy:", "c"), ("Paste", "paste:", "v"), ("Select All", "selectAll:", "a")] {
            edit.addItem(NSMenuItem(title: title, action: NSSelectorFromString(selector), keyEquivalent: key))
        }
        editItem.submenu = edit; main.addItem(editItem); NSApp.mainMenu = main
        // Siblings, not WKWebView under GPUIView: GPUIView.performKeyEquivalent
        // otherwise processes navigation/IME keys before the WebKit responder.
        let view = HostView(frame: gpuiView.frame)
        view.viewer = web
        host.contentView = view
        gpuiView.frame = view.bounds
        gpuiView.autoresizingMask = [.width, .height]
        view.addSubview(gpuiView)
        do {
            web.frame = NSRect(x: 340, y: 0, width: max(0, view.bounds.width - 340), height: view.bounds.height)
            web.autoresizingMask = [.width, .height]
            view.addSubview(web)
        }
        web.navigationDelegate = self
        web.configuration.userContentController.add(self, name: "poc")
        web.loadHTMLString(html, baseURL: nil)
        for name in [NSWindow.didResizeNotification, NSWindow.didChangeBackingPropertiesNotification,
                     NSWindow.didChangeScreenNotification, NSWindow.didBecomeKeyNotification, NSWindow.didResignKeyNotification] {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: host, queue: .main) { [weak self] note in
                MainActor.assumeIsolated { self?.geometry(note.name.rawValue) }
            })
        }
        observers.append(NotificationCenter.default.addObserver(forName: NSApplication.willTerminateNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.cleanup() }
        })
        for name in [NSWorkspace.willSleepNotification, NSWorkspace.didWakeNotification] {
            observers.append(NSWorkspace.shared.notificationCenter.addObserver(forName: name, object: nil, queue: .main) { note in record(note.name.rawValue) })
        }
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            MainActor.assumeIsolated {
                if event.modifierFlags.contains(.command) {
                    if event.charactersIgnoringModifiers == "q" { self?.quitApp(); return nil }
                    if event.charactersIgnoringModifiers == "w" { self?.hideWindow(); return nil }
                }
                record("native.key", ["key_code": event.keyCode, "responder": String(describing: host.firstResponder)])
                return event
            }
        }
        signal(SIGTERM, SIG_IGN)
        termination = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .main)
        termination?.setEventHandler { [weak self] in self?.quitApp() }
        termination?.resume()
        geometry("bridge.ready")
    }
    func geometry(_ event: String) {
        record(event, ["visible": host.isVisible, "window_number": host.windowNumber,
            "width_points": host.contentView?.bounds.width ?? 0, "height_points": host.contentView?.bounds.height ?? 0,
            "web_width_points": web.bounds.width, "scale": host.backingScaleFactor,
            "screens": NSScreen.screens.count])
    }
    @objc func showWindow(_ sender: NSMenuItem) { host.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true); record("open.source", ["source": sender.representedObject as? String ?? "unknown"]); geometry("window.open") }
    @objc func hideWindow() { host.orderOut(nil); geometry("window.hide") }
    @objc func quitApp() { record("quit.request"); cleanup(); NSApp.terminate(nil) }
    func cleanup() {
        guard keyMonitor != nil else { return }
        if let keyMonitor { NSEvent.removeMonitor(keyMonitor) }; keyMonitor = nil
        termination?.cancel(); termination = nil
        web.stopLoading(); web.navigationDelegate = nil
        web.configuration.userContentController.removeScriptMessageHandler(forName: "poc")
        web.removeFromSuperview(); NSStatusBar.system.removeStatusItem(status)
        for observer in observers {
            NotificationCenter.default.removeObserver(observer)
            NSWorkspace.shared.notificationCenter.removeObserver(observer)
        }
        observers.removeAll(); record("bridge.cleaned")
    }
    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        if let details = message.body as? [String: Any] { record("web.input", details) }
    }
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) { geometry("web.ready") }
}
@_cdecl("pot_bridge_install") public func install(_ html: UnsafePointer<CChar>) {
    let content = String(cString: html)
    DispatchQueue.main.async {
        guard let host = NSApp.windows.first(where: { $0.title == "pot macOS shell" }) else {
            record("bridge.error", ["reason": "GPUI host missing"]); NSApp.terminate(nil); return
        }
        bridge = PotBridge(host: host, html: content)
    }
}
@_cdecl("pot_bridge_hide") public func hide() { MainActor.assumeIsolated { bridge?.hideWindow() } }
@_cdecl("pot_bridge_focus_gpui") public func focusGPUI() {
    MainActor.assumeIsolated {
        if let bridge { bridge.host.makeFirstResponder(bridge.gpuiResponder); record("gpui.focus") }
    }
}
@_cdecl("pot_bridge_log") public func logInput(_ event: UnsafePointer<CChar>, _ value: UnsafePointer<CChar>) {
    record("gpui." + String(cString: event), ["value": String(cString: value)])
}
