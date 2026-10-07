import AppKit
import Foundation
import WebKit

var bridge: PotBridge?

/// GPUI renders chat. Native siblings own navigation, IME composer and desktop.
@MainActor final class HostView: NSView {
    var resized: (() -> Void)?
    override func layout() {
        super.layout(); resized?()
    }
}

@MainActor class RailButton: NSButton {
    var selected = false {
        didSet {
            if oldValue != selected {
                refresh()
            }
        }
    }

    private var lastBackground: CGColor?
    fileprivate var hovered = false
    private var tracking: NSTrackingArea?
    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let tracking {
            removeTrackingArea(tracking)
        }
        tracking = NSTrackingArea(
            rect: bounds,
            options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
            owner: self,
            userInfo: nil
        )
        addTrackingArea(tracking!)
    }

    override func mouseEntered(with _: NSEvent) {
        hovered = true; refresh()
    }

    override func mouseExited(with _: NSEvent) {
        hovered = false; refresh()
    }

    func refresh() {
        let color = selected ? NSColor.controlAccentColor
            .withAlphaComponent(hovered ? 0.08 : 0) :
            (hovered ? NSColor.labelColor.withAlphaComponent(0.07) : .clear)
        if lastBackground != color.cgColor {
            let previous = layer?.presentation()?.backgroundColor ?? layer?.backgroundColor
            lastBackground = color.cgColor
            layer?.backgroundColor = color.cgColor
            if !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion, let previous {
                let animation = CABasicAnimation(keyPath: "backgroundColor")
                animation.fromValue = previous; animation.toValue = color.cgColor
                animation.duration = 0.14; layer?.add(animation, forKey: "selection")
            }
        }
        contentTintColor = selected || hovered ? .controlAccentColor : .secondaryLabelColor
    }
}

@MainActor final class PlaceholderLabel: NSTextField {
    override func hitTest(_: NSPoint) -> NSView? {
        nil
    }
}

@MainActor final class Composer: NSTextView {
    var submit: (() -> Void)?
    override func keyDown(with event: NSEvent) {
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
    let rail = NSView()
    let composer = Composer()
    let composerBox = NSView()
    let composerScroll = NSScrollView()
    let placeholder = PlaceholderLabel(labelWithString: L.text("bridge.message-pot"))
    let sendButton = NSButton()
    let settings = SettingsPanel()
    var navigation: [NSButton] = []
    var page = 0
    var composerDark: Bool?
    var webDark: Bool?
    var viewer: [String: Any] = [:]
    var lastUIState: [String: Any] = [:]
    var localRuntime: LocalRuntime?
    var termination: DispatchSourceSignal?
    var keyMonitor: Any?
    let railHighlight = PlaceholderLabel(labelWithString: "")
    var backMenuItem: NSMenuItem?
    var forwardMenuItem: NSMenuItem?
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
    var drafts: [String: String] = [:]
    var running = false
    var ready = false
    var failed = false
    var unread = 0
    var unreadTracker = UnreadTracker()
    var companionCompleted: Set<String>?
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
    var sandboxRefreshing = false
    var sandboxRefreshAt = Date.distantPast
    var desktopAttempt: String?
    var guestIP = ""
    var localPhase = "starting"
    var isApplying = false
    var seenKey: String {
        "seen-assistant:" + (localRuntime?.root.path ?? "default")
    }

    init(host: NSWindow, html _: String) {
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
        rootView.addSubview(rail)
        rail.wantsLayer = true
        railHighlight.wantsLayer = true; railHighlight.layer?.cornerRadius = 10
        rail.addSubview(railHighlight)
        for (index, symbol, title) in [
            (0, "bubble.left", L.text("navigation.chat")),
            (3, "list.bullet.rectangle", L.text("navigation.tasks")),
            (1, "desktopcomputer", "Sandbox"),
            (4, "chart.bar.xaxis", L.text("navigation.usage")),
            (2, "gearshape", L.text("ui.settings")),
        ] {
            let button = index == 0 ? CompanionButton() : RailButton(
                image: NSImage(systemSymbolName: symbol, accessibilityDescription: title)!,
                target: self,
                action: #selector(navigate(_:))
            )
            button.target = self; button.action = #selector(navigate(_:))
            button.title = ""
            button.tag = index; button.toolTip = title; button.setAccessibilityLabel(title)
            button.isBordered = false; button.imageScaling = .scaleProportionallyDown
            button.wantsLayer = true; button.layer?.cornerRadius = 10
            rail.addSubview(button); navigation.append(button)
        }
        rootView.addSubview(web); web.isHidden = true; web.navigationDelegate = self
        web.configuration.userContentController.add(self, name: "potDesktop")
        settings.bridge = self
        rootView.addSubview(composerBox); composerBox.wantsLayer = true; composerBox.layer?
            .cornerRadius = 16; composerBox.layer?.borderWidth = 1
        composer.isRichText = false; composer.font = .systemFont(ofSize: 15); composer.drawsBackground = false; composer
            .textContainerInset = NSSize(
                width: 8,
                height: 10
            )
        composer.isVerticallyResizable = true; composer.isHorizontallyResizable = false
        composer.autoresizingMask = [.width]; composer.textContainer?.widthTracksTextView = true
        composer.delegate = self; composer.submit = { [weak self] in self?.sendMessage() }
        composerScroll.documentView = composer; composerScroll.hasVerticalScroller = true; composerScroll
            .autohidesScrollers = true; composerScroll.drawsBackground = false
        composerBox.addSubview(composerScroll)
        placeholder.textColor = .placeholderTextColor; placeholder.font = .systemFont(ofSize: 15); composerBox
            .addSubview(placeholder)
        sendButton.target = self; sendButton.action = #selector(sendMessage); sendButton.isBordered = false
        sendButton.wantsLayer = true; sendButton.layer?.cornerRadius = 16
        composerBox.addSubview(sendButton)
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
                    case "d": self.selectPage(self.page == 1 ? 0 : 1); return nil
                    case ",": self.selectPage(2); return nil
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
        localRuntime?.onState = { [weak self] state in self?.environmentChanged(state) }
        settings.load()
        NotificationCenter.default.addObserver(
            forName: NSApplication.didBecomeActiveNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                if self?.settings.loginVisible == true {
                    self?.settings.refreshAgents()
                }
            }
        }
        animation = Timer
            .scheduledTimer(withTimeInterval: 0.1, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated { self?.updateIcon() }
            }
        layout(); selectPage(page)
    }

    func localize() {
        lastIconState = ""
        installMenu()
        backMenuItem?.isEnabled = (lastUIState["can_back"] as? Bool) ?? false
        forwardMenuItem?.isEnabled = (lastUIState["can_forward"] as? Bool) ?? false
        let labels = [
            0: "navigation.chat",
            3: "navigation.tasks",
            1: "Sandbox",
            4: "navigation.usage",
            2: "ui.settings",
        ]
        for button in navigation {
            let key = labels[button.tag] ?? "Sandbox"
            let title = key == "Sandbox" ? key : L.text(key)
            button.toolTip = title; button.setAccessibilityLabel(title)
        }
        localizeComposer()
        updateIcon()
        localizeWeb()
    }

    func localizeComposer() {
        placeholder.stringValue = selected == "new" ? L.text("bridge.describe-the-goal-of-a-new-task") : selected
            .isEmpty ? L.text("bridge.message-pot") : L
            .text("bridge.add-instructions-interrupted-tasks-resume-next-turn")
        let stopping = selectedRunning && composer.string.isEmpty
        sendButton.toolTip = stopping ? L.text("bridge.stop-task-41") : L.text("bridge.send-enter-new-line-shift-enter")
        sendButton.setAccessibilityLabel(stopping ? L.text("bridge.stop-task") : L.text("bridge.send-message"))
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
            ("Sandbox", #selector(toggleDesktop), "d"),
            (L.text("bridge.settings"), #selector(openSettings), ","),
            (L.text("bridge.open-workspace"), #selector(openWorkspace), ""),
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
        for (title, action, key) in [
            (L.text("navigation.back"), #selector(historyBack), "["),
            (L.text("navigation.forward"), #selector(historyForward), "]"),
        ] {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
            item.target = self; item.isEnabled = false; navigationMenu.addItem(item)
            if key == "[" {
                backMenuItem = item
            } else {
                forwardMenuItem = item
            }
        }
        NSApp.mainMenu = main
    }

    func navigateHistory(_ forward: Bool) {
        // The embedded desktop and marked input retain their own shortcuts.
        if let view = host.firstResponder as? NSView,
           view.isDescendant(of: web)
        {
            return
        }
        if let input = host.firstResponder as? NSTextInputClient, input.hasMarkedText() {
            return
        }
        events.append(["action": forward ? "history-forward" : "history-back"]); bridgeWake?()
    }

    @objc func showAbout() {
        PotAbout.show()
    }

    @objc func historyBack() {
        navigateHistory(false)
    }

    @objc func historyForward() {
        navigateHistory(true)
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
        rail.frame = NSRect(x: 0, y: 0, width: 56, height: b.height)
        for (index, button) in navigation.enumerated() {
            button.frame = NSRect(
                x: 10,
                y: button.tag == 2 ? 16 : b.height - 60 - CGFloat(index) * 48,
                width: 36,
                height: 36
            )
        }
        let area = NSRect(x: 56, y: 0, width: max(0, b.width - 56), height: b.height)
        // GPUI uses window coordinates; reserve the rail in Rust layout.
        if gpuiView.frame != b {
            gpuiView.frame = b
        }
        // Must match ui::SANDBOX_LIST and shared 56 pt navigation header and two 56 pt desktop toolbar rows.
        let desktopArea = NSRect(x: 280, y: 0, width: max(0, b.width - 280), height: max(0, b.height - 168))
        if web.frame != desktopArea {
            web.frame = desktopArea
        }
        let width = min(760, area.width - 64)
        composer.layoutManager?.ensureLayout(for: composer.textContainer!)
        let used = (composer.layoutManager?.usedRect(for: composer.textContainer!).height ?? 24) +
            (composer.string.hasSuffix("\n") ? composer.layoutManager?.extraLineFragmentRect.height ?? 0 : 0)
        let height = min(150, max(48, used + 20))
        let boxFrame = NSRect(x: 56 + (area.width - width) / 2, y: 24, width: width, height: height)
        if composerBox.frame != boxFrame {
            composerBox.frame = boxFrame
        }
        let scrollFrame = NSRect(x: 8, y: 2, width: width - 58, height: height - 4)
        if composerScroll.frame != scrollFrame {
            composerScroll.frame = scrollFrame
        }
        let textSize = NSSize(width: composerScroll.contentSize.width, height: max(height - 4, used + 20))
        if composer.frame.size != textSize {
            composer.setFrameSize(textSize)
        }
        placeholder.frame = NSRect(x: 18, y: height - 33, width: width - 90, height: 22)
        sendButton.frame = NSRect(x: width - 42, y: 8, width: 32, height: 32)
        let dark = NSApp.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        if webDark != dark {
            webDark = dark
            web.evaluateJavaScript("document.documentElement.dataset.theme='" + (dark ? "dark" : "light") + "'")
        }
        let background = NSColor(white: dark ? 0.125 : 0.992, alpha: 1)
        let sidebar = NSColor(white: dark ? 0.157 : 0.957, alpha: 1)
        rootView.layer?.backgroundColor = background.cgColor
        rail.layer?.backgroundColor = sidebar.cgColor
        composerBox.layer?.backgroundColor = NSColor(white: dark ? 0.169 : 1, alpha: 1).cgColor
        composerBox.layer?.shadowColor = NSColor.black.cgColor
        composerBox.layer?.shadowOpacity = dark ? 0.12 : 0.04
        composerBox.layer?.shadowRadius = 6
        composerBox.layer?.shadowOffset = NSSize(width: 0, height: -1)
        composerBox.layer?.borderColor = NSColor(white: dark ? 0.255 : 0.91, alpha: 1).cgColor
        for button in navigation {
            (button as? RailButton)?.selected = button
                .tag == (page == 6 ? 4 : page == 5 ? (selected.isEmpty ? 0 : 3) : page)
            (button as? RailButton)?.refresh()
        }
        if let selectedButton = navigation.first(where: { ($0 as? RailButton)?.selected == true }) {
            railHighlight.layer?.backgroundColor = NSColor.controlAccentColor.withAlphaComponent(0.12).cgColor
            if railHighlight.frame != selectedButton.frame {
                if railHighlight.frame.isEmpty || NSWorkspace.shared.accessibilityDisplayShouldReduceMotion {
                    railHighlight.frame = selectedButton.frame
                } else {
                    NSAnimationContext.runAnimationGroup { context in
                        context.duration = 0.22
                        context.timingFunction = CAMediaTimingFunction(controlPoints: 0.22, 1, 0.36, 1)
                        railHighlight.animator().frame = selectedButton.frame
                    }
                }
            }
        }
        sendButton.layer?.backgroundColor = (selectedRunning || ready ? NSColor.systemBlue : NSColor(
            white: dark ? 0.28 : 0.8,
            alpha: 1
        )).cgColor
        sendButton.contentTintColor = .white
        // Runtime snapshots arrive every 200 ms. Reassigning text attributes
        // restarts NSTextView's insertion-point blink even when nothing changed.
        if composerDark != dark {
            composerDark = dark
            composer.textColor = .labelColor
            composer.insertionPointColor = .labelColor
        }
    }

    func textDidChange(_: Notification) {
        placeholder.isHidden = !composer.string.isEmpty; layout()
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
        page = value; bridgeWake?(); web.isHidden = value != 1 || localRuntime?.displayReady != true
        gpuiView.isHidden = false; composerBox.isHidden = value != 0 && value != 3
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

    @objc func navigate(_ sender: NSButton) {
        if sender.tag == 0 {
            events.append(["action": "main"])
        }; if sender
            .tag == 3
        {
            events.append(["action": "tasks"])
        }; selectPage(sender.tag)
    }

    /// The toolbar is GPUI, above WKWebView. A web menu cannot extend into
    /// that header, so use an AppKit popup in the same window coordinate space.
    func selectedSandbox() -> [String: Any]? {
        sandboxItems.first { $0["id"] as? String == localRuntime?.sandboxID }
    }

    func sandboxBlocked(_ item: [String: Any]?) -> String? {
        if (item?["active_leases"] as? Int ?? 0) > 0 {
            return L.text("bridge.tasks-or-tools-are-using-this-environment")
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
            (blocked.map { L.text("bridge.cannot-stop") + $0 } ?? L.text("bridge.stop-environment"), "stop"),
            (
                blocked
                    .map { L.text("bridge.cannot-delete") + $0 } ??
                    ((target == "default" || current?["referenced"] as? Bool == true) ? L
                        .text("bridge.cannot-delete-default-environment-or-referenced-by") :
                        L.text("bridge.delete-environment")),
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
            alert.informativeText = tasks.isEmpty ? L.text("bridge.no-tasks-associated-with-this-environment") : tasks
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
            let alert = NSAlert(); alert.messageText = action == "stop" ? L.text("bridge.stop-environment-27") : L
                .text("bridge.delete-environment-28")
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
            sandboxBusy = false; refreshSandboxes(); localRuntime?.poll(); bridgeWake?()
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

    func editSandbox(create: Bool) {
        guard !sandboxBusy, let target = localRuntime?.sandboxID else { return }
        let current = selectedSandbox()
        let alert = NSAlert(); alert.messageText = create ? L.text("bridge.new-linux-sandbox") : L
            .text("ui.environment-settings")
        alert.informativeText = create ? L.text("bridge.the-environment-stays-stopped-until-you-start") : L
            .text("bridge.stop-this-environment-before-changing-its-name")
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
                        self.chooseSandbox(id)
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
        guard !sandboxRefreshing else { return }
        sandboxRefreshing = true; sandboxRefreshAt = Date()
        Task {
            defer { sandboxRefreshing = false }
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
        settings.cancelLogin()
        animation?
            .invalidate(); animation = nil; if let keyMonitor
        {
            NSEvent.removeMonitor(keyMonitor)
        }; keyMonitor = nil; termination?.cancel(); termination = nil; backendShutdown(); localRuntime?.cleanup(); web
            .stopLoading(); web
            .configuration.userContentController.removeScriptMessageHandler(forName: "potDesktop"); NSStatusBar.system
            .removeStatusItem(status)
    }

    func environmentChanged(_ state: [String: Any]) {
        localPhase = state["phase"] as? String ?? "unknown"
        if ["stopped", "failed", "stopping"].contains(localPhase) {
            viewer = [:]; desktopAttempt = nil
        }
        guestIP = state["ip"] as? String ?? ""
        if page == 1 {
            web.isHidden = localRuntime?.displayReady != true
        }
        settings.environment = state; settings.refreshStatus()
        runtime.root = localRuntime?.root; runtime.ip = "127.0.0.1"
        if Date().timeIntervalSince(sandboxRefreshAt) > 1 {
            refreshSandboxes()
        }
        if page == 1 {
            if localPhase == "running", desktopAttempt != localRuntime?.sandboxID, !sandboxBusy {
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
            web.isHidden = page != 1
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
        lastUIState = value
        snapshot = value
        running = value["running"] as? Bool ?? false
        selectedRunning = value["selected_running"] as? Bool ?? false
        if let accepted = value["accepted"] as? String,
           let conversation = value["accepted_conversation"] as? String
        {
            var text = composer.string
            if DraftReceipt.apply(accepted: accepted, conversation: conversation, selected: selected,
                                  text: &text, drafts: &drafts)
            {
                composer.string = text
                textDidChange(Notification(name: NSText.didChangeNotification))
            }
        }
        backMenuItem?.isEnabled = value["can_back"] as? Bool ?? false
        forwardMenuItem?.isEnabled = value["can_forward"] as? Bool ?? false
        let next = value["selected"] as? String ?? ""
        if next != selected {
            drafts[selected] = composer.string
            selected = next; composer.string = drafts[next] ?? ""
            if page == 0, !next.isEmpty {
                page = 3
            }
            textDidChange(Notification(name: NSText.didChangeNotification))
        }
        composerBox.isHidden = (page != 0 && page != 3) || value["task_list"] as? Bool == true
        placeholder.stringValue = next == "new" ? L.text("bridge.describe-the-goal-of-a-new-task") : next.isEmpty ? L
            .text("bridge.message-pot") : L.text("bridge.add-instructions-interrupted-tasks-resume-next-turn")
        if page == 0 && value["needs_auth"] as? Bool == true {
            composerBox.isHidden = true
        }
        ready = (value["can_send"] as? Bool ?? false) && !isApplying
        failed = (value["error"] as? Bool ?? false) || localPhase == "failed"
        sendButton.image = NSImage(
            systemSymbolName: selectedRunning && composer.string.isEmpty ? "stop.fill" : "arrow.up",
            accessibilityDescription: selectedRunning && composer.string.isEmpty ? L.text("bridge.stop-task") : L
                .text("bridge.send-message")
        )
        sendButton.toolTip = selectedRunning && composer.string.isEmpty ? L.text("bridge.stop-task-41") : L
            .text("bridge.send-enter-new-line-shift-enter")
        sendButton.isEnabled = selectedRunning || ready
        if value["has_snapshot"] as? Bool == true, let messages = value["messages"] as? [[String: Any]] {
            let completed = Set(messages.filter { ($0["role"] as? String) == "assistant" && [
                "complete",
                "failed",
                "cancelled",
            ].contains($0["status"] as? String ?? "") }.compactMap { $0["id"] as? String })
            let previous = unreadTracker.seen
            let successful = Set(messages.filter { $0["role"] as? String == "assistant" &&
                    $0["status"] as? String == "complete"
            }.compactMap { $0["id"] as? String })
            if let known = companionCompleted, !successful.subtracting(known).isEmpty {
                (navigation.first as? CompanionButton)?.celebrate()
            }
            companionCompleted = successful
            let failedIDs = Set(messages.filter { $0["status"] as? String == "failed" }
                .compactMap { $0["id"] as? String })
            if !failedIDs.subtracting(previous).isEmpty {
                settings.refreshAgents()
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
        if snapshot["connected"] as? Bool == true, settings.agentStates.isEmpty,
           !settings.discovering
        {
            settings.refreshAgents()
        }
        layout()
    }

    func updateIcon() {
        (navigation.first as? CompanionButton)?.update(cooking: running)
        let reduce = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        let waiting = (snapshot["needs_auth"] as? Bool ?? false)
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
            (failed ? L.text("bridge.environment-error") : waiting ? L.text("bridge.waiting-for-configuration") : "")
        let label = "Pot · \(state)" + (unread > 0 ? L.format("bridge.0-unread", [String(describing: unread)]) : "")
        status.button?.title = ""
        status.button?.toolTip = nil
        status.button?.setAccessibilityLabel(label)
        navigation.first?
            .setAccessibilityLabel(L.format("bridge.chat-0", [String(describing: state)]) + (unread > 0 ? L.format(
                "bridge.0-unread",
                [String(describing: unread)]
            ) : ""))
    }
}

@_cdecl("pot_bridge_install") public func install(_ html: UnsafePointer<CChar>) {
    let content = String(cString: html)
    DispatchQueue.main
        .async { guard let host = NSApp.windows.first(where: { $0.title == "Pot" }) else { return }; bridge = PotBridge(
            host: host,
            html: content
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

@_cdecl("pot_bridge_composer_height") public func composerHeight() -> Double {
    MainActor
        .assumeIsolated { Double(bridge?.composerBox.frame.height ?? 48) + 48 }
}

private var bridgeWake: (@convention(c) () -> Void)?
@_cdecl("pot_bridge_set_wake") public func setWake(_ callback: @escaping @convention(c) () -> Void) {
    bridgeWake = callback
}

@_cdecl("pot_bridge_ui") public func uiState(_ buffer: UnsafeMutablePointer<CChar>, _ capacity: Int32) -> Int32 {
    MainActor.assumeIsolated {
        guard let bridge else { return 0 }
        let state: [String: Any] = [
            "page": bridge.page,
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
        case "resource-command": bridge.resourceCommand(
                v["request_id"] as? String ?? "",
                v["operation"] as? String ?? ""
            )
        case "resource-cache": bridge.showResourceCache()
        case "provider-period": settings.providerPeriod = v["period"] as? String ?? "none"
        case "provider-auto": settings.providerAutoResume.toggle()
        case "provider-save": settings.saveProviderAllowance(tokens: v["tokens"] as? String ?? "")
        case "routing-refresh": settings.refreshRouting()
        case "routing-edit": settings.editRoutingProfile(v["id"] as? String ?? "")
        case "routing-choice": settings.routingChoice(v["kind"] as? String ?? "", v["id"] as? String ?? "")
        case "routing-save": settings.saveRouting(
                profileLimit: v["profile_limit"] as? String ?? "",
                providerLimit: v["provider_limit"] as? String ?? ""
            )
        case "profile-save": settings.saveProfile(
                name: v["name"] as? String ?? "",
                model: v["model"] as? String ?? "",
                copy: v["copy"] as? Bool ?? false
            )
        case "profile-main": settings.useProfileForOwner()
        case "task-profile":
            settings.routingOwner = v["owner"] as? String ?? "main"
            settings.routingOwnerName = v["title"] as? String ?? L.text("bridge.this-task")
            settings.section = 4; bridge.selectPage(2); settings.refreshRouting()
        case "history-section": settings.section = v["value"] as? Int ?? 0
        case "section": settings.section = v["value"] as? Int ?? 0; settings
            .result = ""; settings.returnToChat = false; if settings.section == 1 {
                settings.routingOwner = "main"; settings.aiScreen = "main"
                settings.refreshAgents()
            } else if settings.section == 4 {
                settings.routingOwner = "main"; settings.routingOwnerName = L
                    .text("routing.main-conversation"); settings.refreshRouting()
            }
        case "language":
            if let locale = v["value"] as? String, L.select(locale) {
                bridge.localize(); bridgeWake?()
            }
        case "appearance": settings.changeAppearance(v["value"] as? Int ?? 0)
        case "save-agent":
            settings.key = v["key"] as? String ?? ""; settings.limit = v["limit"] as? String ?? ""; settings
                .tokens = v["tokens"] as? String ?? ""; settings.perTurn = v["per_turn"] as? String ?? ""; settings
                .parallel = v["parallel"] as? String ?? ""; settings.saveAgent(); settings.key = ""
        case "workspace": bridge.openWorkspace()
        case "page": bridge.selectPage(v["value"] as? Int ?? 0)
        case "manage", "sandbox-settings": bridge.editSandbox(create: false)
        case "sandbox-new": bridge.editSandbox(create: true)
        case "sandbox-view": if let id = v["id"] as? String {
                bridge.chooseSandbox(id)
            }
        case "start": bridge.startSandbox()
        case "api-protocol": settings.apiProtocol = v["protocol"] as? String ?? "openai_chat"
        case "ai-open": settings.routingOwner = "main"; settings.openAI(fromChat: true)
        case "ai-back":
            if settings.loginVisible {
                settings.cancelLogin()
            }
            settings.aiScreen = "main"
            if settings.routingOwner != "main" {
                settings.section = 4
            }
        case "ai-screen": if !settings.aiBusy {
                if settings.loginVisible {
                    settings.cancelLogin()
                }
                settings.aiScreen = v["screen"] as? String ?? "main"
            }
        case "ai-new": settings.newConnection(v["kind"] as? String ?? "custom")
        case "ai-manage": settings.editConnection(v["id"] as? String ?? "")
        case "ai-choose": settings.chooseAI(v["id"] as? String ?? "")
        case "ai-api-check": settings.checkAPI(endpoint: v["endpoint"] as? String ?? "", key: v["key"] as? String ?? "")
        case "ai-save-connection": settings.saveConnection(
                name: v["name"] as? String ?? "",
                model: v["model"] as? String ?? "",
                endpoint: v["endpoint"] as? String ?? "",
                key: v["key"] as? String ?? ""
            )
        case "ai-save-main": settings.saveMain(model: v["model"] as? String ?? "")
        case "login-open": settings.openLogin(v["engine"] as? String ?? "")
        case "login-cancel": settings.cancelLogin()
        case "login-reopen": settings.reopenLogin()
        case "agent-refresh": settings.refreshAgents()
        case "save-invocation": settings.saveInvocation(
                executable: v["executable"] as? String ?? "",
                arguments: v["argv"] as? String ?? ""
            )
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

/// Animate only the character, leaving the navigation hit area and highlight stable.
@MainActor final class CompanionButton: RailButton {
    private var cooking = false
    private var visible = false
    private var reduced = false
    private var intro = -Double.infinity
    private var pressed = -Double.infinity
    private var completed = -Double.infinity
    private var hoverChanged = -Double.infinity
    private var hoverFrom: CGFloat = 0
    private var hoverTo: CGFloat = 0
    private var steamChanged = -Double.infinity
    private var steamFrom: CGFloat = 0
    private var steamTo: CGFloat = 0
    private var frames: Timer?

    private func blend(_ from: CGFloat, _ to: CGFloat, since start: Double, at now: Double) -> CGFloat {
        let t = CGFloat(min(1, max(0, (now - start) / 0.18)))
        return from + (to - from) * t * t * (3 - 2 * t)
    }

    func update(cooking next: Bool) {
        let now = ProcessInfo.processInfo.systemUptime
        let onScreen = window?.isVisible == true && window?.occlusionState.contains(.visible) == true
        let reduce = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        if onScreen && !visible {
            intro = now
        }
        if next != cooking {
            steamFrom = blend(steamFrom, steamTo, since: steamChanged, at: now)
            steamTo = next ? 1 : 0; steamChanged = now
            cooking = next
            needsDisplay = true
        }
        if reduce != reduced || onScreen != visible {
            needsDisplay = true
        }
        visible = onScreen; reduced = reduce
        if reduced {
            intro = -.infinity; pressed = -.infinity; completed = -.infinity
            hoverFrom = hoverTo; steamFrom = steamTo
        }
        scheduleFrames()
    }

    private func scheduleFrames() {
        let now = ProcessInfo.processInfo.systemUptime
        let moving = cooking || now - intro < 0.36 || now - pressed < 0.28 ||
            now - completed < 0.85 || now - hoverChanged < 0.18 || now - steamChanged < 0.18
        guard visible, !reduced, moving else {
            frames?.invalidate(); frames = nil
            return
        }
        guard frames == nil else { return }
        let timer = Timer(timeInterval: 1 / 30.0, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self else { return }
                self.update(cooking: self.cooking)
                self.needsDisplay = true
            }
        }
        frames = timer
        RunLoop.main.add(timer, forMode: .common)
    }

    private func animateHover() {
        let now = ProcessInfo.processInfo.systemUptime
        hoverFrom = blend(hoverFrom, hoverTo, since: hoverChanged, at: now)
        hoverTo = hovered ? 1 : 0; hoverChanged = now
        update(cooking: cooking); needsDisplay = true
    }

    func celebrate() {
        guard window?.isVisible == true, !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion else { return }
        completed = ProcessInfo.processInfo.systemUptime
        update(cooking: cooking); needsDisplay = true
    }

    override func mouseEntered(with event: NSEvent) {
        super.mouseEntered(with: event); animateHover()
    }

    override func mouseExited(with event: NSEvent) {
        super.mouseExited(with: event); animateHover()
    }

    override func mouseDown(with event: NSEvent) {
        pressed = ProcessInfo.processInfo.systemUptime
        update(cooking: cooking); needsDisplay = true
        super.mouseDown(with: event)
    }

    override func draw(_ dirtyRect: NSRect) {
        super.draw(dirtyRect)
        let now = ProcessInfo.processInfo.systemUptime
        let hover = reduced ? hoverTo : blend(hoverFrom, hoverTo, since: hoverChanged, at: now)
        let steam = reduced ? steamTo : blend(steamFrom, steamTo, since: steamChanged, at: now)
        let entry = reduced ? 1 : min(1, max(0, (now - intro) / 0.36))
        let pressAge = now - pressed
        let compression = !reduced && pressAge >= 0 && pressAge < 0.28 ? sin(pressAge / 0.28 * .pi) * 0.05 : 0
        let blinkAge = now - intro
        let blink = !reduced && blinkAge > 0.13 && blinkAge < 0.29 ?
            1 - sin((blinkAge - 0.13) / 0.16 * .pi) * 0.95 : 1
        let happy = !reduced && now - completed < 0.85 ? sin((now - completed) / 0.85 * .pi) : 0
        let size = CGFloat(28 * (1 - compression))
        let rect = NSRect(x: (bounds.width - size) / 2,
                          y: (bounds.height - size) / 2 - (reduced ? 0 : hover * 0.7) + CGFloat(1 - entry) * 2,
                          width: size, height: size)
        companionIcon(blink: CGFloat(blink), smile: hover * 0.4 + CGFloat(happy) * 0.6,
                      steam: steam, phase: now.truncatingRemainder(dividingBy: 3) / 3)
            .draw(in: rect, from: .zero, operation: .sourceOver, fraction: CGFloat(0.8 + 0.2 * entry),
                  respectFlipped: true, hints: nil)
    }
}
