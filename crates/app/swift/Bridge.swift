import AppKit
import Foundation
import WebKit

var bridge: PotBridge?

/// GPUI renders product UI. Native siblings supply text editing/IME and desktop services.
@MainActor final class HostView: NSView {
    var resized: (() -> Void)?
    private var previousBounds: NSRect?
    override func layout() {
        super.layout()
        if previousBounds != bounds {
            previousBounds = bounds; resized?(); wakeNativeUI()
        }
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance(); wakeNativeUI()
    }
}

@MainActor final class Composer: NSTextView {
    var submit: (() -> Void)?
    var completeKey: ((NSEvent) -> Bool)?
    override func keyDown(with event: NSEvent) {
        if !hasMarkedText(), completeKey?(event) == true {
            return
        }
        // Route navigation as editing commands, never as printable control codes.
        // Leave marked-text navigation to the input method.
        if !hasMarkedText(), [123, 124, 125, 126, 115, 119].contains(event.keyCode) {
            let shift = event.modifierFlags.contains(.shift)
            let word = event.modifierFlags.contains(.option)
            let edge = event.modifierFlags.contains(.command)
            let action: String = switch event.keyCode {
            case 123: edge ? "moveToBeginningOfLine" : word ? "moveWordLeft" : "moveLeft"
            case 124: edge ? "moveToEndOfLine" : word ? "moveWordRight" : "moveRight"
            case 125: edge ? "moveToEndOfDocument" : word ? "moveToEndOfParagraph" : "moveDown"
            case 126: edge ? "moveToBeginningOfDocument" : word ? "moveToBeginningOfParagraph" : "moveUp"
            case 115: "moveToBeginningOfDocument"
            default: "moveToEndOfDocument"
            }
            let selector = NSSelectorFromString(action + (shift ? "AndModifySelection:" : ":"))
            if responds(to: selector) {
                perform(selector, with: nil)
            }
            return
        }
        if event.keyCode == 36, !event.modifierFlags.contains(.shift), !hasMarkedText() {
            submit?(); return
        }
        super.keyDown(with: event)
    }
}

@MainActor final class PotBridge: NSObject, WKNavigationDelegate, WKScriptMessageHandler, NSTextViewDelegate {
    let host: NSWindow
    let gpuiView: NSView
    let rootView: HostView
    let status: NSStatusItem
    let web: WKWebView
    let composer = Composer()

    var editorGeometry: NSRect?
    var completionActive = false
    let composerBox = NSView()
    let composerScroll = NSScrollView()
    var settings = SettingsPanel()
    var page = 0
    var composerDark: Bool?
    var webDark: Bool?
    var viewer: [String: Any] = [:]
    var lastUIState: [String: Any] = [:]
    var localRuntime: LocalRuntime?
    var termination: DispatchSourceSignal?
    var keyMonitor: Any?
    var backMenuItem: NSMenuItem?
    var animation: Timer?
    var events: [[String: Any]] = [] {
        didSet {
            if events.count > oldValue.count {
                bridgeWake?()
            }
        }
    }

    var snapshot: [String: Any] = [:]
    var selectedRunning = false
    var selected = ""
    var running = false
    var ready = false
    var failed = false
    var unread = 0
    var unreadTracker = UnreadTracker()
    var savedSeen: [String]?
    var tick: Double = 0
    var lastIconState = ""
    var observations: [NSObjectProtocol] = []
    let runtime = RuntimeClient()
    var resourceRequests: [[String: Any]] = []
    var resourceContinuations: [String: () -> Void] = [:]
    var sandboxItems: [[String: Any]] = []
    var sandboxDefault = "default"
    var sandboxBusy = false
    @LocalizedMessage var sandboxError = ""
    var sandboxRefreshAgain = false
    var sandboxRefreshing = false
    var sandboxRefreshAt = Date.distantPast
    var desktopAttempt: String?
    var guestIP = ""
    var localPhase = "starting"
    var isApplying = false
    var seenKey: String {
        "seen-assistant:" + (localRuntime?.root.path ?? "default")
    }

    init(host: NSWindow) {
        self.host = host; gpuiView = host.contentView!
        rootView = HostView(frame: gpuiView.frame)
        web = WKWebView(frame: .zero, configuration: WKWebViewConfiguration())
        status = NSStatusBar.system.statusItem(withLength: 58)
        super.init()
        NSApp.setActivationPolicy(.regular)
        host.isReleasedWhenClosed = false
        host.title = "Pot"; host.titlebarAppearsTransparent = true; host.backgroundColor = .windowBackgroundColor
        rootView.wantsLayer = true
        host.contentView = rootView
        rootView.addSubview(gpuiView)
        rootView.addSubview(web); web.isHidden = true; web.navigationDelegate = self
        web.configuration.userContentController.add(self, name: "potDesktop")
        settings.bridge = self
        rootView.addSubview(composerBox)
        composer.isRichText = false; composer.font = .systemFont(ofSize: 15); composer.drawsBackground = false; composer
            .textContainerInset = NSSize(
                width: 8,
                height: 10
            )
        composer.isVerticallyResizable = true; composer.isHorizontallyResizable = false
        composer.autoresizingMask = [.width]; composer.textContainer?.widthTracksTextView = true
        composer.delegate = self; composer.submit = { [weak self] in self?.sendMessage() }
        composer.completeKey = { [weak self] event in
            guard let self, self.completionActive,
                  !event.modifierFlags.contains(.shift) else { return false }
            let key: String
            switch event
                .keyCode
            {
            case 125: key = "down"; case 126: key = "up"; case 36: key = "enter"; case 53: key =
                "escape"; default: return false
            }
            self.events.append(["action": "composer-key", "key": key]); return true
        }
        composerScroll.documentView = composer; composerScroll.hasVerticalScroller = true; composerScroll
            .autohidesScrollers = true; composerScroll.drawsBackground = false
        composerBox.addSubview(composerScroll)
        rootView.resized = { [weak self] in self?.layout() }
        status.button?.imagePosition = .imageLeading
        status.menu = nil
        status.button?.target = self
        status.button?.action = #selector(showWindow)
        status.button?.sendAction(on: .leftMouseUp)
        installMenu()
        if let url = Bundle.main
            .url(forResource: "Pot", withExtension: "icns")
        {
            NSApp.applicationIconImage = NSImage(contentsOf: url)
        }
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            MainActor.assumeIsolated {
                guard let self else { return event }
                if event.modifierFlags.contains(.command) {
                    if ["[", "]"].contains(event.charactersIgnoringModifiers ?? ""),
                       event.window === self.host,
                       (self.host.firstResponder as? NSTextInputClient)?.hasMarkedText() != true,
                       (self.host.firstResponder as? NSView)?.isDescendant(of: self.web) != true,
                       NSApp.mainMenu?.performKeyEquivalent(with: event) == true
                    {
                        return nil
                    }
                    if let text = self.host.firstResponder as? NSTextView {
                        switch event.charactersIgnoringModifiers {
                        case "a": text.selectAll(nil); return nil
                        case "c": text.copy(nil); return nil
                        case "v": text.paste(nil); return nil
                        case "x": text.cut(nil); return nil
                        case "z": if event.modifierFlags.contains(.shift) {
                                text.undoManager?.redo()
                            } else {
                                text.undoManager?.undo()
                            }; return nil
                        default: break
                        }
                    }
                    switch event.charactersIgnoringModifiers {
                    case "q": self.quitApp(); return nil
                    case "w": self.hideWindow(); return nil
                    case "d": self.events.append(["action": "navigation", "value": self.page == 1 ? 0 : 1]); return nil
                    case ",": self.events.append(["action": "navigation", "value": 2]); return nil
                    case ".": self.events.append(["action": "cancel"]); return nil
                    default: break
                    }
                }
                // GPUI's window also handles key equivalents. Deliver events to
                // the focused native editor before that path can reinterpret them.
                if event.window === self.host, self.host.firstResponder === self.composer {
                    self.composer.keyDown(with: event)
                    return nil
                }
                return event
            }
        }
        signal(SIGTERM, SIG_IGN); termination = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .main)
        termination?.setEventHandler { [weak self] in self?.quitApp() }; termination?.resume()
        localRuntime = LocalRuntime(view: rootView, web: web)
        localRuntime?.onState = { [weak self] state in self?.runtimeChanged(state) }
        settings.load()
        for name in [NSWindow.didBecomeKeyNotification, NSWindow.didResignKeyNotification,
                     NSWindow.didChangeOcclusionStateNotification, NSWindow.didDeminiaturizeNotification]
        {
            NotificationCenter.default.addObserver(forName: name, object: host, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.updateIcon(); wakeNativeUI() }
            }
        }
        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.updateIcon(); wakeNativeUI() }
        }
        layout(); selectPage(page)
    }

    func localize() {
        lastIconState = ""
        installMenu()
        backMenuItem?.isEnabled = (lastUIState["can_back"] as? Bool) ?? false
        composer.setAccessibilityLabel(L.text("mobile.message"))
        updateIcon()
        localizeWeb()
    }

    func localizeWeb() {
        web.evaluateJavaScript("window.potSetLocale?.('" + L.locale + "')")
    }

    func installMenu() {
        let main = NSMenu(); main.autoenablesItems = false; let item = NSMenuItem(); main.addItem(item)
        let menu = NSMenu(title: "Pot"); menu.autoenablesItems = false; item.submenu = menu
        let about = NSMenuItem(title: L.text("menu.about-pot"), action: #selector(showAbout), keyEquivalent: "")
        about.target = self
        menu.addItem(about)
        menu.addItem(.separator())
        for (title, action, key) in [
            (L.text("menu.open-pot"), #selector(showWindow), ""),
            (L.text("menu.hide-pot"), #selector(hideWindow), "h"),
            (L.text("machines.title"), #selector(toggleDesktop), "d"),
            (L.text("bridge.settings"), #selector(openSettings), ","),
            (L.text("menu.quit-pot"), #selector(quitApp), "q"),
        ] {
            let i = NSMenuItem(title: title, action: action, keyEquivalent: key); i.target = self; menu.addItem(i)
        }
        let edit = NSMenu(title: L.text("menu.edit")); edit.autoenablesItems = false; let e = NSMenuItem(); e
            .submenu = edit; main
            .addItem(e)
        for (title, action, key) in [
            (L.text("bridge.undo"), "undo:", "z"),
            (L.text("bridge.cut"), "cut:", "x"),
            (L.text("bridge.copy"), "copy:", "c"),
            (L.text("bridge.paste"), "paste:", "v"),
            (L.text("bridge.select-all"), "selectAll:", "a"),
        ] {
            let item = NSMenuItem(title: title, action: NSSelectorFromString(action), keyEquivalent: key); item
                .representedObject = action; item.action = #selector(editAction(_:)); item.target = self; edit
                .addItem(item)
        }
        let navigationMenu = NSMenu(title: L.text("bridge.navigation")); navigationMenu.autoenablesItems = false
        let navigationItem = NSMenuItem(); navigationItem.submenu = navigationMenu; main.addItem(navigationItem)
        let back = NSMenuItem(title: L.text("navigation.back"), action: #selector(navigateUp), keyEquivalent: "[")
        back.target = self; back.isEnabled = false; navigationMenu.addItem(back)
        backMenuItem = back
        NSApp.mainMenu = main
    }

    @objc func navigateUp() {
        // The embedded desktop and marked input retain their own shortcuts.
        if let view = host.firstResponder as? NSView,
           view.isDescendant(of: web)
        {
            return
        }
        if let input = host.firstResponder as? NSTextInputClient, input.hasMarkedText() {
            return
        }
        events.append(["action": "navigate-up"]); bridgeWake?()
    }

    @objc func showAbout() {
        PotAbout.show()
    }

    @objc func editAction(_ item: NSMenuItem) {
        guard let action = item.representedObject as? String else { return }
        if host.firstResponder === gpuiView {
            if action == "copy:" {
                events.append(["action": "copy-selection"])
            }
            if action == "selectAll:" {
                events.append(["action": "select-all"])
            }
            for (selector, name) in [("paste:", "paste"), ("cut:", "cut"), ("undo:", "undo"), ("redo:", "redo")] {
                if action == selector {
                    events.append(["action": name])
                }
            }
        } else if let responder = host.firstResponder, responder.responds(to: NSSelectorFromString(action)) {
            responder.perform(NSSelectorFromString(action), with: item)
        }
    }

    func layout() {
        let b = rootView.bounds
        let area = NSRect(x: 56, y: 0, width: max(0, b.width - 56), height: b.height)
        // GPUI uses window coordinates; reserve the rail in Rust layout.
        if gpuiView.frame != b {
            gpuiView.frame = b
        }
        // Match the 44 pt titlebar and two 56 pt desktop toolbar rows.
        let desktopArea = NSRect(x: 280, y: 0, width: max(0, b.width - 280), height: max(0, b.height - 156))
        if web.frame != desktopArea {
            web.frame = desktopArea
        }
        let width = editorGeometry?.width ?? min(760, area.width - 64)
        composer.layoutManager?.ensureLayout(for: composer.textContainer!)
        let used = (composer.layoutManager?.usedRect(for: composer.textContainer!).height ?? 24) +
            (composer.string.hasSuffix("\n") ? composer.layoutManager?.extraLineFragmentRect.height ?? 0 : 0)
        let height = editorGeometry?.height ?? 48
        let boxFrame = NSRect(
            x: editorGeometry?.minX ?? 56 + (area.width - width) / 2,
            y: editorGeometry?.minY ?? 24,
            width: (editorGeometry?.width ?? width) - 50,
            height: height
        )
        if composerBox.frame != boxFrame {
            composerBox.frame = boxFrame; wakeNativeUI()
        }
        let scrollFrame = NSRect(x: 8, y: 2, width: width - 58, height: height - 4)
        if composerScroll.frame != scrollFrame {
            composerScroll.frame = scrollFrame
        }
        let textSize = NSSize(width: composerScroll.contentSize.width, height: max(height - 4, used + 20))
        if composer.frame.size != textSize {
            composer.setFrameSize(textSize)
        }
        let dark = NSApp.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        if webDark != dark {
            webDark = dark
            web.evaluateJavaScript("document.documentElement.dataset.theme='" + (dark ? "dark" : "light") + "'")
        }
        let background = NSColor(white: dark ? 32.0 / 255 : 253.0 / 255, alpha: 1)
        host.backgroundColor = background
        rootView.layer?.backgroundColor = background.cgColor
        // Runtime snapshots arrive every 200 ms. Reassigning text attributes
        // restarts NSTextView's insertion-point blink even when nothing changed.
        if composerDark != dark {
            composerDark = dark
            composer.textColor = .labelColor
            composer.insertionPointColor = .labelColor
        }
    }

    func textDidChange(_: Notification) {
        wakeNativeUI(); layout()
    }

    func textViewDidChangeSelection(_: Notification) {
        wakeNativeUI(); layout()
    }

    @objc func sendMessage() {
        if selectedRunning,
           composer.string.trimmingCharacters(in: .whitespacesAndNewlines)
           .isEmpty
        {
            events.append(["action": "cancel"]); return
        }
        guard ready,!isApplying,!composer.string.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        guard composer.string.utf8.count <= 65536
        else {
            let alert = NSAlert(); alert.messageText = L.text("bridge.message-too-long"); alert.informativeText = L
                .text("bridge.shorten-the-message-to-less-than-64"); alert
                .beginSheetModal(for: host); return
        }
        events.append(["action": "send", "text": composer.string])
    }

    func selectPage(_ value: Int) {
        page = value; bridgeWake?(); web.isHidden = value != 1 || machinesDetail || localRuntime?.displayReady != true
        gpuiView.isHidden = false
        composerBox.isHidden = (value != 0 && value != 3) || lastUIState["task_list"] as? Bool == true ||
            lastUIState["assistant_list"] as? Bool == true ||
            (value == 0 && lastUIState["needs_configuration"] as? Bool == true)
        if value == 0 {
            host.makeFirstResponder(composer)
        }
        if value == 1 {
            refreshSandboxes()
        }
        if value == 2 {
            settings.refreshStatus()
        }
        layout()
    }

    /// The toolbar is GPUI, above WKWebView. A web menu cannot extend into
    /// that header, so use an AppKit popup in the same window coordinate space.
    var machinesDetail = true

    func selectedSandbox() -> [String: Any]? {
        sandboxItems.first { $0["id"] as? String == localRuntime?.sandboxID }
    }

    func sandboxBlocked(_ item: [String: Any]?) -> String? {
        if (item?["active_leases"] as? Int ?? 0) > 0 {
            return L.text("bridge.tasks-or-tools-are-using-this-sandbox")
        }
        if (item?["unsettled_operations"] as? Int ?? 0) > 0 {
            return L.text("bridge.some-operations-are-unconfirmed-ask-the-main")
        }
        return nil
    }

    func showSandboxMenu() {
        guard page == 1, let target = localRuntime?.sandboxID else { return }
        let menu = NSMenu(); menu.autoenablesItems = false
        let current = selectedSandbox()
        let blocked = sandboxBlocked(current)
        let connected = viewer["connected"] as? Bool == true
        for (title, id) in [
            (L.text("bridge.reconnect-desktop"), "connect"), (L.text("bridge.open-browser"), "browser"), (
                L.text("bridge.clipboard"),
                "clipboardOpen"
            ),
            ("", ""), (L.text("bridge.open-workspace"), "workspace"), (L.text("bridge.associated-tasks"), "tasks"),
            (L.text("bridge.set-as-execution-default"), "select"), ("", ""),
            (blocked.map { L.text("bridge.cannot-stop") + $0 } ?? L.text("bridge.stop-sandbox"), "stop"),
            (
                blocked
                    .map { L.text("bridge.cannot-delete") + $0 } ??
                    ((target == "default" || current?["referenced"] as? Bool == true) ? L
                        .text("bridge.cannot-delete-default-sandbox-or-referenced-by") :
                        L.text("bridge.delete-sandbox")),
                "delete"
            ),
        ] {
            if id.isEmpty {
                menu.addItem(.separator()); continue
            }
            let item = NSMenuItem(title: title, action: #selector(sandboxMenuAction(_:)), keyEquivalent: "")
            item.target = self; item.representedObject = ["action": id, "target": target]
            item.isEnabled = !sandboxBusy
            if ["connect", "browser", "clipboardOpen"]
                .contains(id)
            {
                item
                    .isEnabled = id == "connect" ? (localPhase == "running") :
                    (connected && viewer["human"] as? Bool == true &&
                        (current?["desktop_capabilities"] as? [String: Any])?[id == "browser" ? "browser" :
                            "clipboard"] as? Bool == true)
            }
            if id == "select" {
                item.isEnabled = target != sandboxDefault
            }
            if id == "stop" {
                item.isEnabled = blocked == nil && localPhase == "running"
            }
            if id ==
                "delete"
            {
                item.isEnabled = blocked == nil && target != "default" && current?["referenced"] as? Bool != true
            }
            menu.addItem(item)
        }
        menu.minimumWidth = 200
        menu.popUp(
            positioning: nil,
            at: NSPoint(x: max(280, rootView.bounds.maxX - 32 - menu.size.width), y: rootView.bounds.maxY - 164),
            in: rootView
        )
    }

    @objc func sandboxMenuAction(_ item: NSMenuItem) {
        guard let info = item.representedObject as? [String: String], let action = info["action"],
              let target = info["target"] else { return }
        switch action {
        case "connect":
            if target == localRuntime?.sandboxID {
                localRuntime?.reconnectDesktop()
            }
        case "workspace": openSandboxWorkspace(target)
        case "tasks":
            let tasks = (sandboxItems.first { $0["id"] as? String == target })?["tasks"] as? [[String: Any]] ?? []
            let alert = NSAlert(); alert.messageText = L.text("bridge.associated-tasks-24")
            alert.informativeText = tasks.isEmpty ? L.text("bridge.no-tasks-associated-with-this-sandbox") : tasks
                .map { L.format(
                    "bridge.associated-task-row",
                    [
                        String(describing: $0["title"] as? String ?? $0["id"] as? String ?? L.text("navigation.tasks")),
                        String(describing: $0["phase"] as? String ?? ""),
                    ]
                ) }
                .joined(separator: "\n")
            alert.addButton(withTitle: L.text("ai.close")); alert.beginSheetModal(for: host)
        case "select": manageSandbox(["action": action, "sandbox_id": target])
        case "stop", "delete":
            let alert = NSAlert(); alert.messageText = action == "stop" ? L.text("bridge.stop-sandbox-27") : L
                .text("bridge.delete-sandbox-28")
            alert.informativeText = L.text("bridge.only-this-sandbox-is-affected-work-files")
            alert.addButton(withTitle: action == "stop" ? L.text("main.stop") : L.text("bridge.delete")); alert
                .addButton(withTitle: L.text("schedules-ui.cancel"))
            alert.beginSheetModal(for: host) { [weak self] reply in MainActor.assumeIsolated {
                if reply == .alertFirstButtonReturn {
                    self?.manageSandbox(["action": action, "sandbox_id": target])
                }
            }}
        default:
            if target == localRuntime?.sandboxID {
                web.evaluateJavaScript(
                    "document.getElementById('\(action)').click()",
                    completionHandler: nil
                )
            }
        }
    }

    func manageSandbox(_ args: [String: Any], completion: (() -> Void)? = nil) {
        guard !sandboxBusy else { return }
        sandboxBusy = true; sandboxError = ""; bridgeWake?()
        Task {
            do {
                let result = try await runtime.request("api/v1/sandboxes", body: args)
                if let key = result["resource_request_id"] as? String {
                    await refreshResourceRequests()
                    resourceContinuations[key] = { [weak self] in
                        if args["action"] as? String != "create" {
                            self?.manageSandbox(args, completion: completion)
                        } else {
                            completion?()
                        }
                    }
                    confirmResourceDownload(key)
                } else {
                    completion?()
                }
                if args["action"] as? String == "delete",
                   args["sandbox_id"] as? String == localRuntime?.sandboxID
                {
                    chooseSandbox("default")
                }
            } catch { sandboxError = error.localizedDescription }
            sandboxBusy = false; refreshSandboxes(); localRuntime?.refreshState(); bridgeWake?()
        }
    }

    func refreshResourceRequests() async {
        if let value = try? await runtime.request("api/v1/sandbox-resources") {
            resourceRequests = value["requests"] as? [[String: Any]] ?? []
            finishResourceRequests(); bridgeWake?()
        }
    }

    func finishResourceRequests() {
        for request in resourceRequests {
            guard let key = request["id"] as? String else { continue }
            if request["phase"] as? String == "ready" {
                resourceContinuations.removeValue(forKey: key)?()
            }
            if request["phase"] as? String == "cancelled" {
                resourceContinuations.removeValue(forKey: key)
            }
        }
    }

    func confirmResourceDownload(_ key: String) {
        guard let request = resourceRequests.first(where: { $0["id"] as? String == key }) else { return }
        let bytes = (request["missing_bytes"] as? NSNumber)?.int64Value ?? 0
        let alert = NSAlert(); alert.messageText = L.text("resources.title")
        alert.informativeText = L.format(
            "resources.permission",
            [
                String(request["bundle_id"] as? String ?? ""),
                ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file),
            ]
        )
        alert.addButton(withTitle: L.text("resources.authorize")); alert
            .addButton(withTitle: L.text("resources.cancel"))
        alert.beginSheetModal(for: host) { [weak self] reply in MainActor.assumeIsolated {
            self?.resourceCommand(key, reply == .alertFirstButtonReturn ? "authorize" : "cancel")
        }}
    }

    func resourceCommand(_ key: String, _ operation: String) {
        Task {
            do { _ = try await runtime.request(
                "api/v1/sandbox-resources",
                body: ["action": operation, "request_id": key]
            ) } catch { sandboxError = error.localizedDescription }
            await refreshResourceRequests(); refreshSandboxes(); bridgeWake?()
        }
    }

    func showResourceCache() {
        Task {
            do {
                let value = try await runtime.request("api/v1/sandbox-resources")
                let rows = (value["cache"] as? [String: Any])?["items"] as? [[String: Any]] ?? []
                let alert = NSAlert(); alert.messageText = L.text("resources.cache")
                alert.informativeText = rows.map { row in
                    let refs = row["references"] as? [String] ?? []
                    let bytes = (row["disk_bytes"] as? NSNumber)?.int64Value ?? 0
                    return "\(row["id"] as? String ?? "") · \(ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file)) · \(refs.joined(separator: ", "))"
                }.joined(separator: "\n")
                let candidates = rows
                    .filter { ($0["present"] as? Bool == true) && (($0["references"] as? [String])?.isEmpty == true) }
                let picker = NSPopUpButton(frame: NSRect(x: 0, y: 0, width: 300, height: 28))
                picker.addItems(withTitles: candidates.map { $0["id"] as? String ?? "" })
                if !candidates
                    .isEmpty
                {
                    alert.accessoryView = picker; alert.addButton(withTitle: L.text("resources.remove"))
                }
                alert.addButton(withTitle: L.text("ai.close"))
                alert.beginSheetModal(for: host) { [weak self] reply in MainActor.assumeIsolated {
                    guard !candidates.isEmpty, reply == .alertFirstButtonReturn, let self else { return }
                    let id = candidates[picker.indexOfSelectedItem]["id"] as? String ?? ""
                    Task { do { _ = try await self.runtime.request(
                        "api/v1/sandbox-resources",
                        body: ["action": "prune", "resource_id": id]
                    ) } catch { self.sandboxError = error.localizedDescription } }
                }}
            } catch { sandboxError = error.localizedDescription }
        }
    }

    func editSandbox(create: Bool, selectCreated: Bool = true) {
        guard !sandboxBusy, let target = localRuntime?.sandboxID else { return }
        let current = selectedSandbox()
        let alert = NSAlert(); alert.messageText = create ? L.text("bridge.new-linux-sandbox") : L
            .text("ui.sandbox-settings")
        alert.informativeText = create ? L.text("bridge.the-sandbox-stays-stopped-until-you-start") : L
            .text("bridge.stop-this-sandbox-before-changing-its-name")
        let box = NSStackView(); box.orientation = .vertical; box.alignment = .leading; box.spacing = 8
        let name = NSTextField(string: create ? L.text("native.linux-sandbox") : current?["name"] as? String ?? "Linux")
        name.frame.size = NSSize(width: 300, height: 24)
        box.addArrangedSubview(NSTextField(labelWithString: L.text("schedules-ui.name"))); box.addArrangedSubview(name)
        let config = current?["config"] as? [String: Any] ?? [:]
        var fields: [NSTextField] = []
        if !create {
            for (title, key, fallback) in [
                ("vCPU (1–32)", "cpus", 4),
                (L.text("bridge.memory-gib-1-128"), "memory_gib", 8),
                (L.text("bridge.system-disk-gib-4-512"), "disk_gib", 32),
            ] {
                let field = NSTextField(string: String(config[key] as? Int ?? fallback)); fields.append(field)
                box.addArrangedSubview(NSTextField(labelWithString: title)); box.addArrangedSubview(field)
            }
        }
        box.frame = NSRect(x: 0, y: 0, width: 300, height: create ? 56 : 224); alert.accessoryView = box
        alert.addButton(withTitle: create ? L.text("bridge.create") : L.text("usage-ui.save")); alert
            .addButton(withTitle: L.text("schedules-ui.cancel"))
        alert.beginSheetModal(for: host) { [weak self] reply in MainActor.assumeIsolated {
            guard reply == .alertFirstButtonReturn, let self else { return }
            if create {
                let id = "linux-" + UUID().uuidString.lowercased().prefix(8)
                self
                    .manageSandbox(["action": "create", "sandbox_id": id, "name": name.stringValue]) {
                        if selectCreated {
                            self.chooseSandbox(id)
                        }
                    }
            } else {
                self.manageSandbox(["action": "update", "sandbox_id": target, "name": name.stringValue,
                                    "config": [
                                        "cpus": Int(fields[0].stringValue) ?? 0,
                                        "memory_gib": Int(fields[1].stringValue) ?? 0,
                                        "disk_gib": Int(fields[2].stringValue) ?? 0,
                                    ]])
            }
        }}
    }

    @objc func toggleDesktop() {
        selectPage(page == 1 ? 0 : 1)
    }

    @objc func openSettings() {
        selectPage(2)
    }

    @objc func showWindow() {
        host.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
    }

    @objc func hideWindow() {
        host.orderOut(nil)
    }

    func refreshSandboxes() {
        guard !sandboxRefreshing else { sandboxRefreshAgain = true; return }
        sandboxRefreshing = true; sandboxRefreshAt = Date()
        Task {
            defer {
                sandboxRefreshing = false
                if sandboxRefreshAgain {
                    sandboxRefreshAgain = false; refreshSandboxes()
                }
            }
            do {
                let value = try await runtime.request("api/v1/sandboxes")
                sandboxItems = value["items"] as? [[String: Any]] ?? []
                sandboxDefault = value["selected"] as? String ?? "default"
                resourceRequests = value["resource_requests"] as? [[String: Any]] ?? []
                finishResourceRequests()
                if let current = selectedSandbox(), let capabilities = current["capabilities"] as? [String: Any] {
                    localRuntime?.desktopSupported = capabilities["desktop"] as? Bool == true
                }
                bridgeWake?()
            } catch { sandboxError = error.localizedDescription; bridgeWake?() }
        }
    }

    func chooseSandbox(_ id: String) {
        guard let localRuntime, id != localRuntime.sandboxID else { return }
        viewer = [:]; localPhase = "unknown"; desktopAttempt = nil; sandboxError = ""
        let item = sandboxItems.first { $0["id"] as? String == id }
        let capabilities = item?["capabilities"] as? [String: Any]
        localRuntime.selectSandbox(id, desktop: capabilities?["desktop"] as? Bool == true); bridgeWake?()
    }

    @objc func startSandbox() {
        guard let localRuntime, localRuntime.desktopSupported, !sandboxBusy else { return }
        let target = localRuntime.sandboxID
        let generation = localRuntime.viewGeneration
        desktopAttempt = target
        sandboxBusy = true; sandboxError = ""; bridgeWake?()
        localRuntime.send("desktop") { [weak self] response in
            guard let self else { return }
            self.sandboxBusy = false
            if let result = response["result"] as? [String: Any], let key = result["resource_request_id"] as? String {
                Task {
                    await self.refreshResourceRequests()
                    self
                        .resourceContinuations[key] = { [weak self] in
                            self?.desktopAttempt = nil; self?.startSandbox()
                        }
                    self.confirmResourceDownload(key)
                }
            }
            if let error = response["error"] as? String {
                self.sandboxError = error
            }
            self.refreshSandboxes(); bridgeWake?()
        }
    }

    func downloadTaskFile(_ task: String, path: String, owner: String) {
        let client = runtime.scopedCopy(); client.assistantID = owner
        Task {
            var temporary: URL?
            do {
                var allowed = CharacterSet.urlQueryAllowed
                allowed.remove(charactersIn: "&+=?#")
                guard let encoded = path.addingPercentEncoding(withAllowedCharacters: allowed),
                      UUID(uuidString: task) != nil else { return }
                let base = "api/v1/tasks/" + task + "/files?path=" + encoded
                let first = try await client.request(base)
                let panel = NSSavePanel(); panel.title = L.text("files.save-title")
                panel.nameFieldStringValue = (path as NSString).lastPathComponent
                guard panel.runModal() == .OK, let destination = panel.url else { return }
                let temp = destination.deletingLastPathComponent()
                    .appendingPathComponent(".pot-download-" + UUID().uuidString)
                temporary = temp
                guard FileManager.default.createFile(
                    atPath: temp.path,
                    contents: nil,
                    attributes: [.posixPermissions: 0o600]
                ) else { throw CocoaError(.fileWriteUnknown) }
                let output = try FileHandle(forWritingTo: temp); defer { try? output.close() }
                guard let size = (first["size"] as? NSNumber)?.uint64Value,
                      size <= 1024 * 1024 * 1024,
                      let revision = first["revision"] as? String,
                      let encodedRevision = revision.addingPercentEncoding(withAllowedCharacters: allowed)
                else { throw CocoaError(.fileReadCorruptFile) }
                var offset: UInt64 = 0
                var chunk = first
                while true {
                    guard let data = (chunk["data"] as? String).flatMap({ Data(base64Encoded: $0) }),
                          data.count <= 192 * 1024,
                          (chunk["offset"] as? NSNumber)?.uint64Value == offset,
                          (chunk["size"] as? NSNumber)?.uint64Value == size, chunk["revision"] as? String == revision,
                          offset + UInt64(data.count) <= size,
                          !data.isEmpty || offset == size else { throw CocoaError(.fileReadCorruptFile) }
                    try output.write(contentsOf: data); offset += UInt64(data.count)
                    if offset == size {
                        break
                    }
                    chunk = try await client
                        .request(base + "&offset=" + String(offset) + "&revision=" + encodedRevision)
                }
                try output.synchronize(); try output.close()
                if FileManager.default.fileExists(atPath: destination.path) {
                    _ = try FileManager.default.replaceItemAt(destination, withItemAt: temp)
                } else {
                    try FileManager.default.moveItem(at: temp, to: destination)
                }
                temporary = nil
            } catch {
                if let temporary {
                    try? FileManager.default.removeItem(at: temporary)
                }
                let alert = NSAlert(); alert.messageText = L.text("files.download-failed"); alert
                    .informativeText = error.localizedDescription
                alert.runModal()
            }
        }
    }

    func openSandboxDesktop(_ target: String) {
        guard let localRuntime, !runtime.remoteMode else { return }
        let client = RuntimeClient(); client.root = localRuntime.root
        Task {
            do {
                let result = try await client.request("api/v1/desktop/sessions", body: ["sandbox_id": target])
                guard let session = result["session"] as? [String: Any],
                      let sessionID = session["id"] as? String, let path = result["path"] as? String,
                      let data = try? Data(contentsOf: localRuntime.root
                          .appendingPathComponent("product/runtime.json")),
                      let endpoint = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let port = endpoint["port"] as? Int,
                      let url = URL(string: "http://127.0.0.1:\(port)\(path)") else { return }
                localRuntime.closeDesktop(); localRuntime.desktopSession = sessionID
                NSWorkspace.shared.open(url)
            } catch { sandboxError = error.localizedDescription; bridgeWake?() }
        }
    }

    @objc func openWorkspace() {
        openSandboxWorkspace(localRuntime?.sandboxID ?? "default")
    }

    func openSandboxWorkspace(_ target: String) {
        if let root = localRuntime?.root {
            let path = root.appendingPathComponent("sandboxes/" + target + "/workspace/workspace")
            do {
                try FileManager.default.createDirectory(at: path, withIntermediateDirectories: true); NSWorkspace.shared
                    .open(path)
            } catch { sandboxError = error.localizedDescription; bridgeWake?() }
        }
    }

    @objc func quitApp() {
        cleanup(); NSApp.terminate(nil)
    }

    func cleanup() {
        animation?
            .invalidate(); animation = nil; if let keyMonitor
        {
            NSEvent.removeMonitor(keyMonitor)
        }; keyMonitor = nil; termination?.cancel(); termination = nil; backendShutdown(); localRuntime?.cleanup(); web
            .stopLoading(); web
            .configuration.userContentController.removeScriptMessageHandler(forName: "potDesktop"); NSStatusBar.system
            .removeStatusItem(status)
    }

    func runtimeChanged(_ state: [String: Any]) {
        localPhase = state["phase"] as? String ?? "unknown"
        if ["stopped", "failed", "stopping"].contains(localPhase) {
            viewer = [:]; desktopAttempt = nil
        }
        guestIP = state["ip"] as? String ?? ""
        if page == 1 {
            web.isHidden = machinesDetail || localRuntime?.displayReady != true
        }
        settings.runtimeStatus = state; settings.refreshStatus()
        runtime.root = localRuntime?.root; runtime.ip = "127.0.0.1"
        if Date().timeIntervalSince(sandboxRefreshAt) > 1 {
            refreshSandboxes()
        }
        if page == 1 {
            if !machinesDetail, localPhase == "running", desktopAttempt != localRuntime?.sandboxID, !sandboxBusy {
                startSandbox()
            }
        }
        bridgeWake?()
    }

    func webView(_: WKWebView, didFinish _: WKNavigation!) {
        webDark = nil; layout(); localizeWeb()
        if let address = localRuntime?.loaded, !address.isEmpty,
           let expected = URL(string: address), web.url?.host == expected.host,
           web.url?.port == expected.port, web.url?.path == expected.path, web.url?.fragment == expected.fragment
        {
            localRuntime?.displayReady = true
            web.isHidden = page != 1 || machinesDetail
        }
    }

    func userContentController(_: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, let loaded = localRuntime?.loaded, let expected = URL(string: loaded),
              let actual = message.frameInfo.request.url, actual.host == expected.host, actual.port == expected.port,
              actual.scheme == expected.scheme, actual.path == expected.path, actual.fragment == expected.fragment,
              let body = message.body as? [String: Any],
              let action = body["action"] as? String else { return }
        switch action {
        case "viewer-state": viewer = body; bridgeWake?()
        case "workspace": openWorkspace()
        case "manage": editSandbox(create: false)
        case "stop", "restart": showSandboxMenu()
        default: break
        }
    }

    func update(_ value: [String: Any]) {
        if let nextPage = value["page"] as? Int, page != nextPage {
            page = nextPage
        }
        runtime.assistantID = value["assistant_id"] as? String ?? "main"
        runtime.remoteMode = value["client_mode"] as? String == "remote"
        lastUIState = value
        snapshot = value
        running = value["running"] as? Bool ?? false
        selectedRunning = value["selected_running"] as? Bool ?? false
        backMenuItem?.isEnabled = value["can_back"] as? Bool ?? false
        let next = value["selected"] as? String ?? ""
        selected = next
        if page == 0, !next.isEmpty {
            page = 3
        }
        composerBox
            .isHidden = (page != 0 && page != 3) || value["task_list"] as? Bool == true ||
            value["assistant_list"] as? Bool == true
        if page == 0 && value["needs_configuration"] as? Bool == true {
            composerBox.isHidden = true
        }
        wakeNativeUI()
        ready = (value["can_send"] as? Bool ?? false) && !isApplying
        failed = (value["error"] as? Bool ?? false) || localPhase == "failed"
        if value["has_snapshot"] as? Bool == true, let messages = value["messages"] as? [[String: Any]] {
            let completed = Set(messages.filter { ($0["role"] as? String) == "assistant" && [
                "complete",
                "failed",
                "cancelled",
            ].contains($0["status"] as? String ?? "") }.compactMap { $0["id"] as? String })
            let previous = unreadTracker.seen
            let failedIDs = Set(messages.filter { $0["status"] as? String == "failed" }
                .compactMap { $0["id"] as? String })
            if !failedIDs.subtracting(previous).isEmpty {
                wakeNativeUI()
            }
            if !unreadTracker.initialized {
                savedSeen = UserDefaults.standard.stringArray(forKey: seenKey)
            }
            unread = unreadTracker.update(
                completed: completed,
                saved: savedSeen,
                readingLatest: selected.isEmpty && value["task_list"] as? Bool != true && page == 0 && host
                    .isKeyWindow && host.isVisible && value["at_bottom"] as? Bool == true
            )
            if previous != unreadTracker
                .seen || savedSeen == nil
            {
                savedSeen = Array(unreadTracker.seen); UserDefaults.standard.set(
                    savedSeen,
                    forKey: seenKey
                )
            }
        }
        settings.refreshStatus()
        updateIcon(); layout()
    }

    func updateIcon() {
        let animate = running && !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        if animate && animation == nil {
            animation = Timer.scheduledTimer(withTimeInterval: 0.1, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated { self?.updateIcon() }
            }
        } else if !animate {
            animation?.invalidate(); animation = nil
        }
        let reduce = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        let waiting = (snapshot["needs_configuration"] as? Bool ?? false)
        let highlighted = status.button?.cell?.isHighlighted ?? false
        let signature = "\(running)-\(unread)-\(waiting)-\(failed)-\(reduce)-\(highlighted)-\(NSApp.effectiveAppearance.name.rawValue)"
        guard signature != lastIconState || (running && !reduce) else { return }
        lastIconState = signature
        tick += 0.1
        let phase = running && !reduce ? (sin(tick * .pi * 2 / 2.7) + 1) / 2 : 0.5
        status.button?.image = trayIcon(
            cooking: running,
            phase: phase,
            waiting: waiting,
            failed: failed,
            unread: unread,
            highlighted: highlighted
        )
        let state = (running ? L.text("client.working") : L.text("bridge.idle")) +
            (failed ? L.text("bridge.runtime-error") : waiting ? L.text("bridge.waiting-for-configuration") : "")
        let label = "Pot · \(state)" + (unread > 0 ? L.format("bridge.0-unread", [String(describing: unread)]) : "")
        status.button?.title = ""
        status.button?.toolTip = nil
        status.button?.setAccessibilityLabel(label)
    }
}

@_cdecl("pot_bridge_install") public func install() {
    DispatchQueue.main
        .async { guard let host = NSApp.windows.first(where: { $0.title == "Pot" }) else { return }; bridge = PotBridge(
            host: host
        ) }
}

@_cdecl("pot_bridge_hide") public func hide() {
    MainActor.assumeIsolated { bridge?.hideWindow() }
}

@_cdecl("pot_bridge_update") public func update(_ json: UnsafePointer<CChar>) {
    MainActor
        .assumeIsolated {
            if let data = String(cString: json).data(using: .utf8),
               let value = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
            {
                bridge?.update(value)
            }
        }
}

@_cdecl("pot_bridge_event") public func nextEvent(_ buffer: UnsafeMutablePointer<CChar>, _ capacity: Int32) -> Int32 {
    MainActor.assumeIsolated {
        guard let bridge,!bridge.events.isEmpty,
              let data = try? JSONSerialization.data(withJSONObject: bridge.events[0]),
              data.count < Int(capacity) else { return 0 }
        bridge.events.removeFirst(); data
            .withUnsafeBytes { memcpy(buffer, $0.baseAddress!, data.count)
            }; buffer[data.count] = 0; return Int32(data.count)
    }
}

@_cdecl("pot_bridge_chinese") public func chinese() -> Bool {
    MainActor.assumeIsolated { L.locale == "zh-CN" }
}

@_cdecl("pot_bridge_dark") public func dark() -> Bool {
    MainActor
        .assumeIsolated { NSApp.effectiveAppearance.bestMatch(from: [
            .darkAqua,
            .aqua,
        ]) == .darkAqua }
}

/// Coalesced by the Rust channel; asynchronous configuration updates must not
/// wait for the geometry/snapshot fallback timer to become visible.
@MainActor func wakeNativeUI() {
    bridgeWake?()
}

private var bridgeWake: (@convention(c) () -> Void)?
@_cdecl("pot_bridge_set_wake") public func setWake(_ callback: @escaping @convention(c) () -> Void) {
    bridgeWake = callback
}

@_cdecl("pot_bridge_ui") public func uiState(_ buffer: UnsafeMutablePointer<CChar>, _ capacity: Int32) -> Int32 {
    MainActor.assumeIsolated {
        guard let bridge else { return 0 }
        let state: [String: Any] = [
            "unread": bridge.unread,
            "editor": [
                "text": bridge.composer.string,
                "caret": (bridge.composer.string as NSString).substring(to: min(
                    bridge.composer.selectedRange().location,
                    (bridge.composer.string as NSString).length
                )).utf8.count,
                "selection": bridge.composer.selectedRange().length > 0,
                "composing": bridge.composer.hasMarkedText(),
            ],
            "page": bridge.page,
            "visible": bridge.host.isVisible && bridge.host.occlusionState.contains(.visible),
            "timezone": TimeZone.current.identifier,
            "settings": bridge.settings.publicState,
            "viewer": bridge.viewer,
            "phase": bridge.localPhase,
            "sandboxes": bridge.sandboxItems,
            "resource_requests": bridge.resourceRequests,
            "sandbox_view": bridge.localRuntime?.sandboxID ?? "default",
            "sandbox_default": bridge.sandboxDefault,
            "sandbox_busy": bridge.sandboxBusy,
            "sandbox_error": bridge.sandboxError,
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: state),
              data.count < Int(capacity) else { return 0 }
        data
            .withUnsafeBytes { memcpy(buffer, $0.baseAddress!, data.count)
            }; buffer[data.count] = 0; return Int32(data.count)
    }
}

@_cdecl("pot_bridge_command") public func uiCommand(_ json: UnsafePointer<CChar>) {
    MainActor.assumeIsolated {
        guard let bridge, let data = String(cString: json).data(using: .utf8),
              let v = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
              let action = v["action"] as? String else { return }
        let settings = bridge.settings
        switch action {
        case "backend-changed":
            let domains = v["domains"] as? [String] ?? []
            let mode = v["mode"] as? String ?? "local"
            let matchesMode = (mode == "remote") == bridge.runtime.remoteMode
            if mode == "local",
               domains.contains(where: { $0 == "machines" || $0 == "resources" })
            {
                bridge.localRuntime?.refreshState()
            }
            if matchesMode {
                if domains.contains(where: { $0 == "machines" || $0 == "resources" }) {
                    bridge.refreshSandboxes()
                }
            }
        case "resource-command": bridge.resourceCommand(
                v["request_id"] as? String ?? "",
                v["operation"] as? String ?? ""
            )
        case "resource-cache": bridge.showResourceCache()
        case "language":
            if let locale = v["value"] as? String, L.select(locale) {
                bridge.localize(); bridgeWake?()
            }
        case "appearance": settings.changeAppearance(v["value"] as? Int ?? 0)
        case "save-agent":
            settings.limit = v["limit"] as? String ?? ""; settings
                .tokens = v["tokens"] as? String ?? ""; settings.perTurn = v["per_turn"] as? String ?? ""; settings
                .parallel = v["parallel"] as? String ?? ""; settings.saveAgent()
        case "workspace": bridge.openWorkspace()
        case "mode-changed":
            bridge.runtime.remoteMode = v["mode"] as? String == "remote"
            bridge.localRuntime?.viewGeneration += 1; bridge.web.stopLoading(); bridge.web.isHidden = true
            bridge.settings.refreshStatus()
        case "editor-geometry":
            let frame = NSRect(
                x: v["x"] as? Double ?? 0,
                y: v["y"] as? Double ?? 24,
                width: v["width"] as? Double ?? 0,
                height: v["height"] as? Double ?? 48
            )
            if bridge.editorGeometry != frame {
                bridge.editorGeometry = frame; bridge.layout()
            }
        case "completion-active": bridge.completionActive = v["value"] as? Bool ?? false
        case "editor-set":
            let text = v["text"] as? String ?? ""
            if bridge.composer.string != text {
                bridge.composer.string = text; bridge.layout(); wakeNativeUI()
            }
        case "editor-replace":
            let text = bridge.composer.string
            let start = min(max(0, v["start"] as? Int ?? 0), text.utf8.count)
            let end = min(max(start, v["end"] as? Int ?? start), text.utf8.count)
            if let a = String.Index(text.utf8.index(text.utf8.startIndex, offsetBy: start), within: text),
               let b = String.Index(
                   text.utf8.index(text.utf8.startIndex, offsetBy: end),
                   within: text
               )
            {
                bridge.composer
                    .insertText(v["text"] as? String ?? "", replacementRange: NSRange(a ..< b, in: text)); bridge.host
                    .makeFirstResponder(bridge.composer); bridge.layout(); wakeNativeUI()
            }
        case "composer-send": bridge.sendMessage()
        case "page": bridge.selectPage(v["value"] as? Int ?? 0)
        case "manage", "sandbox-settings": bridge.editSandbox(create: false)
        case "sandbox-new": bridge.editSandbox(create: true)
        case "sandbox-onboarding": bridge.editSandbox(create: true, selectCreated: false)
        case "machines-detail": bridge.machinesDetail = v["value"] as? Bool ?? true; bridge.web.isHidden = bridge
            .machinesDetail || bridge.page != 1 || bridge.localRuntime?.displayReady != true
        case "sandbox-view": if let id = v["id"] as? String {
                bridge.chooseSandbox(id)
            }
        case "file-download": if let task = v["task_id"] as? String, let path = v["path"] as? String,
                                 let owner = v["owner"] as? String
            {
                bridge.downloadTaskFile(task, path: path, owner: owner)
            }
        case "execution-target-desktop": if let id = v["id"] as? String {
                bridge.openSandboxDesktop(id)
            }
        case "start": bridge.startSandbox()
        case "viewer":
            if let id = v["id"] as? String, ["take", "info", "more", "connect"].contains(id) {
                if id == "more" {
                    bridge.showSandboxMenu()
                } else {
                    bridge.web.evaluateJavaScript("document.getElementById('\(id)').click()", completionHandler: nil)
                }
            }
        default: break
        }
        bridgeWake?()
    }
}

@_cdecl("pot_bridge_reduce_motion") public func reduceMotion() -> Bool {
    MainActor.assumeIsolated { NSWorkspace.shared.accessibilityDisplayShouldReduceMotion }
}
