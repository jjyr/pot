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

@MainActor final class RailButton: NSButton {
    var selected = false {
        didSet { refresh() }
    }

    private var hovered = false
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
            .withAlphaComponent(hovered ? 0.20 : 0.12) :
            (hovered ? NSColor.labelColor.withAlphaComponent(0.07) : .clear)
        layer?.backgroundColor = color.cgColor
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
    let placeholder = PlaceholderLabel(labelWithString: "给 Pot 发消息…")
    let sendButton = NSButton()
    let settings = SettingsPanel()
    var navigation: [NSButton] = []
    var page = 0
    var composerDark: Bool?
    var webDark: Bool?
    var viewer: [String: Any] = [:]
    var localRuntime: LocalRuntime?
    var termination: DispatchSourceSignal?
    var keyMonitor: Any?
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
    var savedSeen: [String]?
    var tick: Double = 0
    var observations: [NSObjectProtocol] = []
    let terminal = SandboxTerminal()
    var terminalSelected = false
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
        status = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        super.init()
        NSApp.setActivationPolicy(.regular)
        host.isReleasedWhenClosed = false
        host.title = "Pot"; host.titlebarAppearsTransparent = true; host.backgroundColor = .windowBackgroundColor
        rootView.wantsLayer = true
        host.contentView = rootView
        rootView.addSubview(gpuiView)
        rootView.addSubview(rail)
        rail.wantsLayer = true
        for (index, symbol, title) in [
            (0, "bubble.left", "聊天"),
            (3, "list.bullet.rectangle", "任务"),
            (1, "desktopcomputer", "Sandbox"),
            (4, "chart.bar.xaxis", "用量"),
            (2, "gearshape", "设置"),
        ] {
            let button = RailButton(
                image: NSImage(systemSymbolName: symbol, accessibilityDescription: title)!,
                target: self,
                action: #selector(navigate(_:))
            )
            button.tag = index; button.toolTip = title; button.setAccessibilityLabel(title)
            button.isBordered = false; button.imageScaling = .scaleProportionallyDown
            button.wantsLayer = true; button.layer?.cornerRadius = 10
            rail.addSubview(button); navigation.append(button)
        }
        rootView.addSubview(terminal.web)
        rootView.addSubview(web); web.isHidden = true; web.navigationDelegate = self
        web.configuration.userContentController.add(self, name: "potDesktop")
        settings.bridge = self
        rootView.addSubview(composerBox); composerBox.wantsLayer = true; composerBox.layer?
            .cornerRadius = 22; composerBox.layer?.borderWidth = 1
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
        if !settings.hasConfiguration {
            selectPage(2); settings.showAgent(setup: true)
        }
        animation = Timer
            .scheduledTimer(withTimeInterval: 0.1, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated { self?.updateIcon() }
            }
        layout(); selectPage(page)
    }

    func installMenu() {
        let main = NSMenu(); main.autoenablesItems = false; let item = NSMenuItem(); main.addItem(item)
        let menu = NSMenu(title: "Pot"); menu.autoenablesItems = false; item.submenu = menu
        for (title, action, key) in [
            ("Open Pot", #selector(showWindow), ""),
            ("Hide Pot", #selector(hideWindow), "h"),
            ("Sandbox", #selector(toggleDesktop), "d"),
            ("设置…", #selector(openSettings), ","),
            ("打开工作目录", #selector(openWorkspace), ""),
            ("Quit Pot", #selector(quitApp), "q"),
        ] {
            let i = NSMenuItem(title: title, action: action, keyEquivalent: key); i.target = self; menu.addItem(i)
        }
        let edit = NSMenu(title: "Edit"); edit.autoenablesItems = false; let e = NSMenuItem(); e.submenu = edit; main
            .addItem(e)
        for (title, action, key) in [
            ("撤销", "undo:", "z"),
            ("剪切", "cut:", "x"),
            ("复制", "copy:", "c"),
            ("粘贴", "paste:", "v"),
            ("全选", "selectAll:", "a"),
        ] {
            let item = NSMenuItem(title: title, action: NSSelectorFromString(action), keyEquivalent: key); item
                .representedObject = action; item.action = #selector(editAction(_:)); item.target = self; edit
                .addItem(item)
        }
        NSApp.mainMenu = main
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
        for button in navigation {
            button.frame = NSRect(
                x: 8,
                y: button.tag == 2 ? 16 : b
                    .height - 60 - CGFloat(button.tag == 3 ? 1 : button.tag == 1 ? 2 : button.tag == 4 ? 3 : 0) * 48,
                width: 40,
                height: 40
            )
        }
        let area = NSRect(x: 56, y: 0, width: max(0, b.width - 56), height: b.height)
        // GPUI uses window coordinates; reserve the rail in Rust layout.
        if gpuiView.frame != b {
            gpuiView.frame = b
        }
        let desktopArea = NSRect(x: 56, y: 0, width: area.width, height: max(0, area.height - 112))
        if web.frame != desktopArea {
            web.frame = desktopArea
        }
        terminal.web.frame = desktopArea
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
        let background = NSColor(white: dark ? 0.10 : 1, alpha: 1)
        let sidebar = NSColor(white: dark ? 0.13 : 0.97, alpha: 1)
        rootView.layer?.backgroundColor = background.cgColor
        rail.layer?.backgroundColor = sidebar.cgColor
        composerBox.layer?.backgroundColor = background.cgColor
        composerBox.layer?.borderColor = NSColor(white: dark ? 0.28 : 0.87, alpha: 1).cgColor
        for button in navigation {
            (button as? RailButton)?.selected = button
                .tag == (page == 6 ? 4 : page == 5 ? (selected.isEmpty ? 0 : 3) : page)
        }
        sendButton.layer?.backgroundColor = (running || ready ? NSColor.systemBlue : NSColor(
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
            let alert = NSAlert(); alert.messageText = "消息过长"; alert.informativeText = "请将消息缩短至 64 KB 以内再发送。"; alert
                .beginSheetModal(for: host); return
        }
        events.append(["action": "send", "text": composer.string])
    }

    func selectPage(_ value: Int) {
        page = value; bridgeWake?(); web.isHidden = value != 1 || terminalSelected || localRuntime?.loaded
            .isEmpty != false
        terminal.show(value == 1 && terminalSelected, root: localRuntime?.root, ip: guestIP)
        gpuiView.isHidden = false; composerBox.isHidden = value != 0 && value != 3
        if value == 0 {
            host.makeFirstResponder(composer)
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
    func showSandboxMenu() {
        guard page == 1, localPhase == "running" else { return }
        let menu = NSMenu(); menu.autoenablesItems = false
        let human = viewer["human"] as? Bool == true
        for (title, id) in [
            ("重新连接桌面", "connect"),
            ("打开浏览器", "browser"),
            ("剪贴板…", "clipboardOpen"),
            ("", ""),
            ("打开工作目录", "workspace"),
            ("管理环境…", "manage"),
            ("", ""),
            ("停止环境…", "stop"),
            ("重启环境…", "restart"),
        ] {
            if id.isEmpty {
                menu.addItem(.separator()); continue
            }
            let item = NSMenuItem(title: title, action: #selector(sandboxMenuAction(_:)), keyEquivalent: "")
            item.target = self; item.representedObject = id
            item.isEnabled = !["browser", "clipboardOpen"].contains(id) || human
            menu.addItem(item)
        }
        menu.minimumWidth = 200
        // Toolbar: 32 pt right inset, 24 pt top inset, 32 pt button height.
        // Include AppKit popup padding so the visible menu clears the button.
        let x = max(56, rootView.bounds.maxX - 32 - menu.size.width)
        menu.popUp(positioning: nil, at: NSPoint(x: x, y: rootView.bounds.maxY - 68), in: rootView)
    }

    @objc func sandboxMenuAction(_ item: NSMenuItem) {
        guard page == 1, let id = item.representedObject as? String,
              ["connect", "browser", "clipboardOpen", "workspace", "manage", "stop", "restart"].contains(id)
        else { return }
        web.evaluateJavaScript("document.getElementById('\(id)').click()", completionHandler: nil)
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

    @objc func startSandbox() {
        localRuntime?.send("start")
    }

    @objc func openWorkspace() {
        if let root = localRuntime?.root {
            NSWorkspace.shared.open(root.appendingPathComponent("workspace/workspace"))
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
        }; keyMonitor = nil; termination?.cancel(); termination = nil; localRuntime?.cleanup(); web.stopLoading(); web
            .configuration.userContentController.removeScriptMessageHandler(forName: "potDesktop"); NSStatusBar.system
            .removeStatusItem(status)
    }

    func environmentChanged(_ state: [String: Any]) {
        localPhase = state["phase"] as? String ?? "unknown"
        guestIP = state["ip"] as? String ?? ""
        if page == 1 {
            web.isHidden = terminalSelected || localRuntime?.loaded.isEmpty != false
        }
        settings.environment = state; settings.refreshStatus()
    }

    func webView(_: WKWebView, didFinish _: WKNavigation!) {
        webDark = nil; layout(); if page == 1 {
            web.isHidden = terminalSelected
        }
    }

    func userContentController(_: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, let loaded = localRuntime?.loaded, let expected = URL(string: loaded),
              let actual = message.frameInfo.request.url, actual.host == expected.host, actual.port == expected.port,
              actual.scheme == expected.scheme, let body = message.body as? [String: Any],
              let action = body["action"] as? String else { return }
        switch action {
        case "viewer-state": viewer = body; bridgeWake?()
        case "workspace": openWorkspace()
        case "manage": selectPage(2); settings.section = 3
        case "stop", "restart":
            guard !isApplying else { return }
            let alert = NSAlert(); alert.messageText = action == "stop" ? "停止 Sandbox？" : "重启 Sandbox？"
            alert.informativeText = running ? "有任务正在运行，将被中断。已保存的聊天与工作文件保留。" : "聊天与工作文件保留，浏览器将正常关闭。"
            alert.addButton(withTitle: action == "stop" ? "停止" : "重启"); alert.addButton(withTitle: "取消")
            alert
                .beginSheetModal(for: host) { [weak self] response in
                    MainActor
                        .assumeIsolated { guard response == .alertFirstButtonReturn,
                                                let self
                            else { return }; self.isApplying = true; self.localRuntime?
                                .send("stop") { [weak self] reply in
                                    guard let self else { return }; if action == "restart",
                                                                       let state = reply["result"] as? [String: Any],
                                                                       state["phase"] as? String ==
                                                                       "stopped"
                                    {
                                        self.localRuntime?
                                            .send("start") { _ in
                                                self.isApplying = false
                                            }
                                    } else {
                                        self.isApplying = false
                                    }
                                }
                        }
                }
        default: break
        }
    }

    func update(_ value: [String: Any]) {
        snapshot = value
        running = value["running"] as? Bool ?? false
        selectedRunning = value["selected_running"] as? Bool ?? false
        if let accepted = value["accepted"] as? String,
           accepted == composer
           .string
        {
            composer.string = ""; drafts[selected] = ""; textDidChange(Notification(name: NSText.didChangeNotification))
        }
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
        placeholder.stringValue = next == "new" ? "描述新任务的目标…" : next.isEmpty ? "给 Pot 发消息…" : "追加指导；中断任务将在下一轮继续…"
        ready = (value["can_send"] as? Bool ?? false) && !isApplying
        failed = (value["error"] as? Bool ?? false) || localPhase == "failed"
        sendButton.image = NSImage(
            systemSymbolName: selectedRunning && composer.string.isEmpty ? "stop.fill" : "arrow.up",
            accessibilityDescription: selectedRunning && composer.string.isEmpty ? "停止任务" : "发送消息"
        )
        sendButton.toolTip = selectedRunning && composer.string.isEmpty ? "停止任务（⌘.）" : "发送（Enter）；换行（Shift+Enter）"
        sendButton.isEnabled = selectedRunning || ready
        if let accepted = value["accepted"] as? String,
           accepted == composer
           .string
        {
            composer.string = ""; textDidChange(Notification(name: NSText.didChangeNotification))
        }
        if value["has_snapshot"] as? Bool == true, let messages = value["messages"] as? [[String: Any]] {
            let completed = Set(messages.filter { ($0["role"] as? String) == "assistant" && [
                "complete",
                "failed",
                "cancelled",
            ].contains($0["status"] as? String ?? "") }.compactMap { $0["id"] as? String })
            let previous = unreadTracker.seen
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
        settings.refreshStatus(); layout()
    }

    func updateIcon() {
        tick += 0.1
        let reduce = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        let lift = running ? (reduce ? 1 : (sin(tick * .pi) + 1) / 2) : 0
        let waiting = (snapshot["needs_auth"] as? Bool ?? false)
        let icon = potIcon(lift: lift, waiting: waiting)
        status.button?.image = icon; navigation.first?.image = icon
        let state = failed ? "环境异常" : waiting ? "等待你完成配置" : running ? "正在处理" : "待命"
        let title = (failed ? " !" : "") + (unread > 0 ? " \(unread > 99 ? "99+" : String(unread))" : "")
        status.button?.title = title; status.button?.toolTip = "Pot · \(state)" + (unread > 0 ? " · \(unread) 条未读" : "")
        navigation.first?.toolTip = unread > 0 ? "聊天 · \(unread) 条未读" : "聊天"
        navigation.first?.setAccessibilityLabel(unread > 0 ? "聊天 · \(unread) 条未读" : "聊天")
    }
}

@MainActor func potIcon(lift: Double = 0, waiting: Bool = false) -> NSImage {
    let image = NSImage(size: NSSize(width: 22, height: 22), flipped: false) { _ in
        NSColor.labelColor.setStroke()
        let bowl = NSBezierPath(); bowl.move(to: NSPoint(x: 4, y: 12)); bowl.line(to: NSPoint(x: 5, y: 6)); bowl.curve(
            to: NSPoint(x: 17, y: 6),
            controlPoint1: NSPoint(x: 6, y: 1),
            controlPoint2: NSPoint(x: 16, y: 1)
        ); bowl.line(to: NSPoint(x: 18, y: 12)); bowl.lineWidth = 1.8; bowl.lineCapStyle = .round; bowl.stroke()
        let lid = NSBezierPath(); let y = 15 + lift * 2
        lid.move(to: NSPoint(x: 4, y: y + (waiting ? 1 : 0))); lid
            .line(to: NSPoint(x: 18, y: y - (waiting ? 1 : 0))); lid.lineWidth = 1.8; lid.lineCapStyle = .round; lid
            .stroke()
        if waiting {
            NSColor.labelColor.setFill(); NSBezierPath(ovalIn: NSRect(x: 10, y: 11, width: 2, height: 2)).fill()
        }
        return true
    }; image.isTemplate = true; return image
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
        case "section": settings.section = v["value"] as? Int ?? 0; settings.setup = false; settings
            .result = ""; if settings.section == 2 {
                settings.refreshAgents()
            }
        case "appearance": settings.changeAppearance(v["value"] as? Int ?? 0)
        case "save-agent":
            settings.key = v["key"] as? String ?? ""; settings.limit = v["limit"] as? String ?? ""; settings
                .tokens = v["tokens"] as? String ?? ""; settings.perTurn = v["per_turn"] as? String ?? ""; settings
                .parallel = v["parallel"] as? String ?? ""; settings.saveAgent(); settings.key = ""
        case "save-sandbox":
            settings.cpuField = v["cpus"] as? String ?? ""; settings
                .memoryField = v["memory"] as? String ?? ""; settings.diskField = v["disk"] as? String ?? ""; settings
                .saveSandbox()
        case "workspace": bridge.openWorkspace()
        case "page": bridge.selectPage(v["value"] as? Int ?? 0)
        case "manage": settings.section = 3; bridge.selectPage(2)
        case "start": bridge.startSandbox()
        case "terminal":
            bridge.terminalSelected = v["value"] as? Bool ?? true; bridge.selectPage(1)
        case "edit-login": settings.editLogin(v["id"] as? String ?? "")
        case "save-login": settings.saveLogin(
                executable: v["executable"] as? String ?? "",
                arguments: v["argv"] as? String ?? ""
            )
        case "api-protocol": settings.apiProtocol = v["protocol"] as? String ?? "openai_chat"
        case "save-api": settings.saveAPI(
                endpoint: v["endpoint"] as? String ?? "",
                key: v["key"] as? String ?? "",
                model: v["model"] as? String ?? ""
            )
        case "select-claude": settings.selectNative(engine: "claude", model: v["model"] as? String ?? "")
        case "select-codex": settings.selectNative(engine: "codex", model: v["model"] as? String ?? "")
        case "agent-refresh": settings.refreshAgents()
        case "agent-login":
            if let agent = v["id"] as? String {
                bridge.terminalSelected = true; bridge.selectPage(1); bridge.terminal.login(agent)
            }
        case "save-invocation": settings.saveInvocation(
                executable: v["executable"] as? String ?? "",
                arguments: v["argv"] as? String ?? ""
            )
        case "terminal-shell": bridge.terminal.shell()
        case "terminal-close": bridge.terminal.close()
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
