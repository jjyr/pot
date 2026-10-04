import AppKit
import Foundation

/// Configuration service only. GPUI Kit owns the settings interface.
@MainActor final class SettingsPanel: NSObject {
    weak var bridge: PotBridge?
    var agentStates: [[String: Any]] = []
    var invocationExecutable = "pi", invocationArgs = "[]", codexModel = "", claudeModel = ""
    var profile: [String: Any] = [:]
    var loginAgent = "", loginExecutable = "", loginArguments = "[]"
    var loginTool: [String: Any] = [:]
    var lastDiscovery = Date.distantPast
    var discovering = false
    var environment: [String: Any] = [:]
    var section = 0, revision = 0
    var setup = false, hasConfiguration = false
    var config: [String: Any] = [:]
    var awaitingBoot: String?
    var cpu = 4, memory = 8, disk = 32
    var key = "", limit = "10000", tokens = "8192", perTurn = "100", parallel = "5"
    var cpuField = "4", memoryField = "8", diskField = "32", result = ""
    var root: URL {
        bridge?.localRuntime?.root ?? URL(fileURLWithPath: NSHomeDirectory() + "/Library/Application Support/pot")
    }

    override init() {
        super.init(); changeAppearance(UserDefaults.standard.integer(forKey: "appearance"))
    }

    func showAgent(setup: Bool = false) {
        self.setup = setup; section = 1
    }

    func changeAppearance(_ value: Int) {
        guard (0 ... 2).contains(value) else { return }
        UserDefaults.standard.set(value, forKey: "appearance")
        NSApp.appearance = value == 0 ? nil : NSAppearance(named: value == 1 ? .aqua : .darkAqua)
    }

    func refreshStatus() {
        guard let bridge else { return }
        if bridge.snapshot["connected"] as? Bool == true,
           let engine = bridge.snapshot["engine"] as? [String: Any],
           let phase = engine["phase"] as? String, phase != "needs_auth"
        {
            setup = false
        }
        if let old = awaitingBoot, let boot = bridge.snapshot["boot_id"] as? String, boot != old,
           bridge.snapshot["connected"] as? Bool == true,
           bridge.localPhase == "running"
        {
            result = "配置已应用，环境已就绪。"; awaitingBoot = nil
        }
    }

    var publicState: [String: Any] {
        ["revision": revision, "section": section, "setup": setup, "has_key": hasConfiguration,
         "limit": limit, "tokens": tokens, "per_turn": perTurn, "parallel": parallel, "cpus": cpuField,
         "memory": memoryField, "disk": diskField,
         "login_agent": loginAgent, "login_executable": loginExecutable, "login_arguments": loginArguments,
         "selected_engine": profile["engine"] as? String ?? "pi", "selected_model": profile["model"] as? String ?? "",
         "codex_model": codexModel, "claude_model": claudeModel, "agents": agentStates, "discovering": discovering,
         "executable": invocationExecutable, "argv": invocationArgs,
         "appearance": UserDefaults.standard.integer(forKey: "appearance"), "result": result]
    }

    func refreshAgents() {
        guard let bridge, !discovering, Date().timeIntervalSince(lastDiscovery) > 1 else { return }
        lastDiscovery = Date()
        bridge.terminal.show(false, root: root, ip: bridge.guestIP)
        discovering = true
        Task {
            defer { discovering = false }
            do {
                let facts = try await bridge.terminal.request("api/v1/agents/refresh", body: [:])
                agentStates = facts["agents"] as? [[String: Any]] ?? []
                let agent = try await bridge.terminal.request("api/v1/agent")
                let bindings = agent["bindings"] as? [[String: Any]] ?? []
                let active = bindings.first { $0["state"] as? String == "active" }
                let selectedID = (active?["spec"] as? [String: Any])?["source_profile_id"] as? String
                if let profiles = agent["profiles"] as? [[String: Any]],
                   let value = profiles.first(where: { $0["id"] as? String == selectedID }) ?? profiles.first
                {
                    profile = value
                    if value["engine"] as? String == "codex" {
                        codexModel = value["model"] as? String ?? ""
                    }
                    if value["engine"] as? String == "claude" {
                        claudeModel = value["model"] as? String ?? ""
                    }
                    let launch = value["launch_spec"] as? [String: Any] ?? [:]
                    invocationExecutable = launch["executable"] as? String ?? "pi"
                    let argv = launch["argv"] as? [[String: Any]] ?? []
                    let literals = argv.compactMap { $0["kind"] as? String == "literal" ? $0["value"] as? String : nil }
                    if let data = try? JSONSerialization.data(withJSONObject: literals), let text = String(
                        data: data,
                        encoding: .utf8
                    ) {
                        invocationArgs = text
                    }
                    revision += 1
                }
                result = "状态已刷新。已登录不代表模型调用已验证。"
            } catch { result = "状态检查失败；可在 Sandbox Terminal 中手动使用 CLI。" }
        }
    }

    func editLogin(_ agent: String) {
        guard let bridge else { return }
        Task {
            do {
                let catalog = try await bridge.terminal.request("api/v1/agents/tools")
                let tools = catalog["tools"] as? [[String: Any]] ?? []
                guard let tool = tools.first(where: { $0["id"] as? String == agent }),
                      let login = tool["login"] as? [String: Any] else { return }
                loginTool = tool; loginAgent = agent
                loginExecutable = login["executable"] as? String ?? ""
                let data = try JSONSerialization.data(withJSONObject: login["argv"] as? [String] ?? [])
                loginArguments = String(data: data, encoding: .utf8) ?? "[]"
                revision += 1
            } catch { result = "登录命令读取失败：\(error.localizedDescription)" }
        }
    }

    func saveLogin(executable: String, arguments: String) {
        guard let bridge, !loginTool.isEmpty, let data = arguments.data(using: .utf8),
              let args = try? JSONSerialization.jsonObject(with: data) as? [String]
        else {
            result = "登录参数必须是 JSON 字符串数组。"; return
        }
        var tool = loginTool
        tool["login"] = ["executable": executable, "argv": args]
        Task {
            do {
                _ = try await bridge.terminal.request("api/v1/agents/tools", body: tool)
                loginTool = tool; loginExecutable = executable; loginArguments = arguments
                result = "登录命令已保存；下次新建登录终端时生效。"
                revision += 1
            } catch { result = "登录命令未保存：\(error.localizedDescription)" }
        }
    }

    func selectNative(engine: String, model: String) {
        let name = engine == "claude" ? "Claude" : "Codex"
        let model = model.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let bridge, !model.isEmpty else { result = "请填写 \(name) 模型名称。"; return }
        bridge.terminal.show(false, root: root, ip: bridge.guestIP)
        Task {
            do {
                let state = try await bridge.terminal.request("api/v1/agent")
                let profiles = state["profiles"] as? [[String: Any]] ?? []
                let previous = profiles.first { $0["id"] as? String == "\(engine)-default" }
                let revision = previous?["revision"] as? Int ?? 0
                let profile: [String: Any] = [
                    "id": "\(engine)-default", "revision": revision + 1, "name": name,
                    "engine": engine, "provider": engine == "claude" ? "anthropic" : "openai", "model": model,
                    "auth_ref": "cli:\(engine)",
                    "params": ["max_output_tokens": 8192],
                    "tool_policy": ["worker_orchestration": false, "summary_read_only": true, "unattended": "guest_vm"],
                ]
                _ = try await bridge.terminal.request("api/v1/agent", body: [
                    "profile": profile, "expected_revision": revision,
                    "request_id": UUID().uuidString, "follow_main": true,
                ])
                if engine == "claude" {
                    claudeModel = model
                } else {
                    codexModel = model
                }; setup = false
                result = "已选择 \(name)；现有轮次结束后生效。请先在 Sandbox Terminal 完成登录。"
                refreshAgents()
            } catch { result = "\(name) 配置未保存：\(error.localizedDescription)" }
        }
    }

    func saveInvocation(executable: String, arguments: String) {
        guard let bridge, let data = arguments.data(using: .utf8),
              let args = try? JSONSerialization.jsonObject(with: data) as? [String],
              let revision = profile["revision"] as? Int else { result = "参数必须是 JSON 字符串数组，且需先配置 API profile。"; return }
        var updated = profile
        updated["revision"] = revision + 1
        updated["launch_spec"] = [
            "transport": profile["engine"] as? String == "codex" ? "codex_app_server" :
                (profile["engine"] as? String == "claude" ? "claude_json" : "pi_rpc"),
            "executable": executable,
            "argv": args
                .map { ["kind": "literal", "value": $0] } + [["kind": "runtime_arguments"]],
            "cwd_policy": "workspace",
        ]
        Task {
            do {
                _ = try await bridge.terminal.request(
                    "api/v1/agent",
                    body: [
                        "profile": updated,
                        "expected_revision": revision,
                        "request_id": UUID().uuidString,
                        "follow_main": true,
                    ]
                )
                result = "命令已保存；在轮次边界迁移，未调用模型。"; refreshAgents()
            } catch { result = "命令未保存：\(error.localizedDescription)" }
        }
    }

    func load() {
        config = ((try? Data(contentsOf: root.appendingPathComponent("workspace/product/pi.json")))
            .flatMap { try? JSONSerialization.jsonObject(with: $0) } as? [String: Any]) ?? [:]
        hasConfiguration = !(config["api_key"] as? String ?? "").isEmpty
        key = ""
        limit = String(config["request_limit"] as? Int ?? 10000)
        tokens = String(config["max_tokens"] as? Int ?? 8192)
        perTurn = String(config["max_requests_per_turn"] as? Int ?? 100)
        parallel = String(config["max_parallel_tasks"] as? Int ?? 5)
        if let data = try? Data(contentsOf: root.appendingPathComponent("sandbox.json")),
           let value = (try? JSONSerialization.jsonObject(with: data)) as? [String: Int]
        {
            cpu = value["cpus"] ?? 4; memory = value["memory_gib"] ?? 8; disk = value["disk_gib"] ?? 32
        }
        cpuField = String(cpu); memoryField = String(memory); diskField = String(disk)
        revision += 1
    }

    func parsed(_ field: String, _ range: ClosedRange<Int>) -> Int? {
        guard let value = Int(field),
              range.contains(value)
        else { return nil }; return value
    }

    @objc func saveAgent() {
        guard let l = parsed(limit, 0 ... 1_000_000), let t = TokenCount.parse(tokens), let p = parsed(
            perTurn,
            1 ... 1000
        ), let n = parsed(parallel, 1 ... 1000) else { result = "请检查请求上限；输出 token 可填正整数或 K/M，例如 128K、1M、1.5M。"; return }
        let credential = key.isEmpty ? config["api_key"] as? String ?? "" : key
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard !credential.isEmpty,
              !credential.contains("\n") else { result = "需要有效的 DeepSeek API key；密钥不会显示或写入日志。"; return }
        let value: [String: Any] = [
            "api_key": credential,
            "model": "deepseek-v4-flash",
            "request_limit": l,
            "max_tokens": t,
            "max_requests_per_turn": p,
            "max_parallel_tasks": n,
        ]
        apply(value, to: root.appendingPathComponent("workspace/product/pi.json"))
    }

    @objc func saveSandbox() {
        guard let c = parsed(cpuField, 1 ... 32), let m = parsed(memoryField, 1 ... 128), let d = parsed(
            diskField,
            4 ... 512
        ) else { result = "请输入范围内的 CPU、内存与系统盘容量。"; return }
        apply(["cpus": c, "memory_gib": m, "disk_gib": d], to: root.appendingPathComponent("sandbox.json"))
    }

    func apply(_ value: [String: Any], to path: URL) {
        guard let bridge,!bridge.running,!bridge.isApplying else { result = "任务仍在运行或环境正在更新，请结束后再应用。"; return }
        guard ["running", "stopped", "failed"].contains(bridge.localPhase) else { result = "环境正在切换状态，请稍后再试。"; return }
        // A disconnected Runtime cannot prove that there is no active task.
        guard bridge.localPhase != "running" || bridge.snapshot["connected"] as? Bool == true
        else { result = "Runtime 尚未连接，无法确认任务状态，请稍后重试。"; return }
        let alert = NSAlert(); alert.messageText = setup ? "保存配置并启动 Pot？" : "应用配置并重启 Sandbox？"
        alert.informativeText = "当前没有运行中的任务。聊天、工作文件和用量记录会保留；浏览器将正常关闭。此操作不调用模型。"
        alert.addButton(withTitle: "应用"); alert.addButton(withTitle: "取消")
        let performApply: (NSApplication.ModalResponse) -> Void = { [weak self] response in
            MainActor.assumeIsolated {
                guard response == .alertFirstButtonReturn, let self, let bridge = self.bridge else { return }
                guard !bridge.running else { self.result = "刚有任务开始，请完成后再应用。"; return }
                bridge.isApplying = true; bridge.ready = false; bridge.sendButton.isEnabled = false
                self.result = "正在正常停止环境并保存配置…"
                guard let runtime = bridge.localRuntime else { return }
                runtime.send("stop") { [weak self] response in
                    guard let self, let bridge = self.bridge else { return }
                    guard let state = response["result"] as? [String: Any],
                          state["phase"] as? String == "stopped"
                    else { bridge.isApplying = false; self.result = "停止失败，配置未修改。"; return }
                    do {
                        try FileManager.default.createDirectory(
                            at: path.deletingLastPathComponent(),
                            withIntermediateDirectories: true
                        )
                        let data = try JSONSerialization.data(withJSONObject: value, options: .sortedKeys)
                        // Atomic replacement and mode 0600; never include credentials in UI errors.
                        let temp = path.deletingLastPathComponent()
                            .appendingPathComponent(".config-" + UUID().uuidString)
                        guard FileManager.default.createFile(
                            atPath: temp.path,
                            contents: nil,
                            attributes: [.posixPermissions: 0o600]
                        ) else { throw NSError(domain: "Pot", code: 1) }
                        defer { try? FileManager.default.removeItem(at: temp) }
                        let handle = try FileHandle(forWritingTo: temp); try handle.write(contentsOf: data); try handle
                            .synchronize(); try handle.close()
                        guard rename(temp.path, path.path) == 0 else { throw NSError(domain: "Pot", code: 2) }
                        self.awaitingBoot = bridge.snapshot["boot_id"] as? String ?? ""
                        self.load(); self.result = "配置已保存，正在启动环境…"; self.setup = false
                        runtime.send("start") { _ in bridge.isApplying = false }
                    } catch { bridge.isApplying = false; self.result = "保存失败，原配置保留。可从 Sandbox 重新启动环境。" }
                }
            }
        }
        if setup {
            performApply(.alertFirstButtonReturn)
        } else {
            alert.beginSheetModal(for: bridge.host, completionHandler: performApply)
        }
    }

    @objc func openWorkspace() {
        bridge?.openWorkspace()
    }
}
