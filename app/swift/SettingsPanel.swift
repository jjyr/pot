import AppKit
import Foundation

/// Configuration service only. GPUI Kit owns the settings interface.
@MainActor final class SettingsPanel: NSObject {
    weak var bridge: PotBridge?
    var aiScreen = "main", draftProfile = "", draftModel = "", connectionKind = "", connectionName = "",
        connectionModel = ""
    var secretRevision = 0, draftRevision = 0
    var connectionDraftID = ""
    var returnToChat = false, aiBusy = false, draftInitialized = false
    var activeSpec: [String: Any] = [:], connections: [[String: Any]] = [], modelOptions: [String] = []
    var authorizationTask: Task<Void, Never>?
    var authorizationID = "", authorizationURL = ""
    var authorizationRelay: AuthorizationRelay?
    var loginEngine = "", loginVisible = false, loginMessage = "", checkMessage = ""
    var agentStates: [[String: Any]] = []
    var invocationExecutable = "pi", invocationArgs = "[]", codexModel = "", claudeModel = "", agyModel = ""
    var profile: [String: Any] = [:]
    var savedProfiles: [[String: Any]] = []
    var routingPolicy: [String: Any] = [:]
    var routingProfile = "", routingName = "", routingModel = ""
    var routingProfileLimit = "", routingProviderLimit = ""
    var routingBusy = false
    var routingOwner = "main", routingOwnerName = "主会话"
    var providerAllowances: [[String: Any]] = []
    var providerAllowance: [String: Any] = [:]
    var providerPeriod = "none", providerTokens = "2000000", providerAutoResume = true
    var apiEndpoint = "", apiModel = "", apiProtocol = "openai_chat", hasAPIKey = false
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
    var setupStage = 0
    var setupItems = Set(["codex", "claude", "agy", "api"])
    var setupAutoPending = false
    var setupKey: String {
        "onboarding.\(root.path)"
    }

    var setupCompleted: Bool {
        UserDefaults.standard.bool(forKey: setupKey + ".complete")
    }

    func persistSetup() {
        UserDefaults.standard.set(setupStage, forKey: setupKey + ".stage")
        UserDefaults.standard.set(Array(setupItems), forKey: setupKey + ".items")
    }

    func finishSetup() {
        UserDefaults.standard.set(true, forKey: setupKey + ".complete")
        setup = false; setupAutoPending = false
        bridge?.selectPage(0)
    }

    func setupNavigate(_ stage: Int) {
        if stage == 1 {
            setup = false; section = 1; returnToChat = true
        } else {
            setup = true; setupStage = 0; section = 2
        }
        setupAutoPending = false; persistSetup(); bridge?.selectPage(2); refreshAgents()
    }

    func deferSetup() {
        UserDefaults.standard.set(true, forKey: setupKey + ".complete")
        setup = false; setupAutoPending = false; bridge?.selectPage(0)
    }

    func setupCanStart(_ engine: String) -> Bool {
        guard let agent = agentStates.first(where: { $0["id"] as? String == engine }),
              agent["installation"] as? String == "present",
              agent["status_check"] as? String != "failed" else { return false }
        let status = agent["authentication"] as? String
        return status == "authenticated" || status == "unknown"
    }

    var root: URL {
        bridge?.localRuntime?.root ?? URL(fileURLWithPath: NSHomeDirectory() + "/Library/Application Support/pot")
    }

    override init() {
        super.init(); changeAppearance(UserDefaults.standard.integer(forKey: "appearance"))
    }

    func showAgent(setup: Bool = false) {
        self.setup = false; section = 1; returnToChat = setup
        if setup {
            setupAutoPending = false
            setupStage = 0
            if let saved = UserDefaults.standard.stringArray(forKey: setupKey + ".items") {
                setupItems = Set(saved)
            }
        }
    }

    func changeAppearance(_ value: Int) {
        guard (0 ... 2).contains(value) else { return }
        UserDefaults.standard.set(value, forKey: "appearance")
        NSApp.appearance = value == 0 ? nil : NSAppearance(named: value == 1 ? .aqua : .darkAqua)
    }

    func refreshStatus() {
        guard let bridge else { return }
        if let old = awaitingBoot, let boot = bridge.snapshot["boot_id"] as? String, boot != old,
           bridge.snapshot["connected"] as? Bool == true,
           bridge.localPhase == "running"
        {
            result = "配置已应用，环境已就绪。"; awaitingBoot = nil
        }
    }

    var publicState: [String: Any] {
        ["revision": revision, "section": section, "setup": setup,
         "ai_screen": aiScreen, "secret_revision": secretRevision, "draft_revision": draftRevision,
         "draft_profile": draftProfile,
         "draft_model": draftModel,
         "connection_profile_id": profile["id"] as? String ?? "", "connection_kind": connectionKind,
         "connection_name": connectionName, "connection_model": connectionModel,
         "return_to_chat": returnToChat, "ai_busy": aiBusy, "active_spec": activeSpec,
         "active_name": savedProfiles
             .first { $0["id"] as? String == activeSpec["source_profile_id"] as? String }?["name"] as? String ??
             (activeSpec["engine"] as? String ?? "Agent"),
         "connections": connections, "model_options": modelOptions,
         "login_engine": loginEngine,
         "login_visible": loginVisible,
         "login_message": loginMessage,
         "check_message": checkMessage,
         "profiles": savedProfiles, "routing": routingPolicy, "routing_profile": routingProfile,
         "routing_name": routingName, "routing_model": routingModel,
         "routing_profile_limit": routingProfileLimit, "routing_provider_limit": routingProviderLimit,
         "routing_owner": routingOwner, "routing_owner_name": routingOwnerName, "routing_busy": routingBusy,
         "provider_allowance": providerAllowance,
         "provider_period": providerPeriod, "provider_tokens": providerTokens,
         "provider_auto_resume": providerAutoResume,
         "setup_stage": setupStage, "setup_items": Array(setupItems),
         "runtime_ready": bridge?.snapshot["connected"] as? Bool ?? false,
         "environment_phase": environment["phase"] as? String ?? "等待启动", "has_key": hasConfiguration,
         "limit": limit, "tokens": tokens, "per_turn": perTurn, "parallel": parallel, "cpus": cpuField,
         "memory": memoryField, "disk": diskField,
         "selected_engine": profile["engine"] as? String ?? "pi", "selected_model": profile["model"] as? String ?? "",
         "api_endpoint": apiEndpoint, "api_model": apiModel, "api_protocol": apiProtocol, "has_api_key": hasAPIKey,
         "codex_model": codexModel, "claude_model": claudeModel, "agy_model": agyModel, "agents": agentStates,
         "discovering": discovering,
         "executable": invocationExecutable, "argv": invocationArgs,
         "appearance": UserDefaults.standard.integer(forKey: "appearance"), "result": result]
    }

    func refreshAgents() {
        guard let bridge, !discovering, bridge.snapshot["connected"] as? Bool == true,
              Date().timeIntervalSince(lastDiscovery) > 1 else { return }
        lastDiscovery = Date(); discovering = true
        Task {
            defer { discovering = false }
            do {
                let facts = try await bridge.runtime.request("api/v1/agents/refresh", body: [:])
                agentStates = facts["agents"] as? [[String: Any]] ?? []
                if ["codex", "grok"].contains(connectionKind),
                   let agent = agentStates.first(where: { $0["id"] as? String == connectionKind })
                {
                    modelOptions = agent["models"] as? [String] ?? []
                    if connectionModel.isEmpty, let first = modelOptions.first {
                        connectionModel = first; draftRevision += 1
                    }
                }
                try await reloadAI()
                if setup, setupAutoPending, !activeSpec.isEmpty {
                    finishSetup()
                }
                result = "状态已刷新；未发送模型请求。"
            } catch { result = "暂时无法检测状态，请检查环境并重试。"; loginMessage = result }
        }
    }

    func saveInvocation(executable: String, arguments: String) {
        guard let bridge, let data = arguments.data(using: .utf8),
              let args = try? JSONSerialization.jsonObject(with: data) as? [String],
              let revision = profile["revision"] as? Int else { result = "参数必须是 JSON 字符串数组，且需先保存连接。"; return }
        var updated = profile
        updated["revision"] = revision + 1
        updated["launch_spec"] = [
            "transport": profile["engine"] as? String == "grok" ? "grok_acp" : profile["engine"] as? String == "codex" ?
                "codex_app_server" :
                (profile["engine"] as? String == "agy" ? "agy_json" :
                    (profile["engine"] as? String == "claude" ? "claude_json" : "pi_rpc")),
            "executable": executable,
            "argv": args
                .map { ["kind": "literal", "value": $0] } + [["kind": "runtime_arguments"]],
            "cwd_policy": "workspace",
        ]
        Task {
            do {
                _ = try await bridge.runtime.request(
                    "api/v1/agent",
                    body: [
                        "profile": updated,
                        "expected_revision": revision,
                        "request_id": UUID().uuidString,
                        "follow_main": false,
                        "follow_worker": false,
                    ]
                )
                result = "命令已保存；在 AI 配置中保存聊天选择后生效。"; refreshAgents()
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
        if let data = try? Data(contentsOf: root.appendingPathComponent("workspace/product/execution-limits.json")),
           let limits = try? JSONSerialization.jsonObject(with: data) as? [String: Int]
        {
            limit = String(limits["request_limit"] ?? 10000); tokens = String(limits["max_tokens"] ?? 8192)
            perTurn = String(limits["max_requests_per_turn"] ?? 100); parallel =
                String(limits["max_parallel_tasks"] ?? 5)
        }
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
        apply(
            ["request_limit": l, "max_tokens": t, "max_requests_per_turn": p, "max_parallel_tasks": n],
            to: root.appendingPathComponent("workspace/product/execution-limits.json")
        )
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

@MainActor extension SettingsPanel {
    func refreshRouting() {
        guard let bridge, !routingBusy else { return }
        routingBusy = true
        Task {
            defer { routingBusy = false }
            do {
                let state = try await bridge.runtime.request("api/v1/agent")
                savedProfiles = state["profiles"] as? [[String: Any]] ?? []
                routingPolicy = try await bridge.runtime.request("api/v1/agent/routing")
                providerAllowances = try await bridge.runtime
                    .request("api/v1/usage/providers")["providers"] as? [[String: Any]] ?? []
                if !savedProfiles.contains(where: { $0["id"] as? String == routingProfile }) {
                    routingProfile = savedProfiles.first?["id"] as? String ?? ""
                }
                editRoutingProfile(routingProfile)
                result = "配置已加载。已有任务保持创建时的选择；汇总沿用主会话。"
            } catch { result = "配置读取失败：\(error.localizedDescription)" }
        }
    }

    func editRoutingProfile(_ id: String) {
        guard let selected = savedProfiles.first(where: { $0["id"] as? String == id }) else { return }
        routingProfile = id
        routingName = selected["name"] as? String ?? ""
        routingModel = selected["model"] as? String ?? ""
        let provider = selected["provider"] as? String ?? ""
        providerAllowance = providerAllowances.first { $0["provider"] as? String == provider } ?? [:]
        let quota = providerAllowance["policy"] as? [String: Any] ?? [:]
        providerPeriod = quota["period"] as? String ?? "none"
        providerTokens = (quota["limit"] as? UInt64).map(String.init) ?? "2000000"
        providerAutoResume = quota["auto_resume"] as? Bool ?? true
        routingProfileLimit = (routingPolicy["profile_limits"] as? [String: Int])?[id].map(String.init) ?? ""
        routingProviderLimit = (routingPolicy["provider_limits"] as? [String: Int])?[provider].map(String.init) ?? ""
        revision += 1
    }

    func routingChoice(_ kind: String, _ id: String) {
        if kind == "worker" {
            routingPolicy["worker_profile"] = id.isEmpty ? NSNull() : id as Any
            routingPolicy["follow_chat"] = id.isEmpty
        } else {
            var allowed = Set(routingPolicy["allowed_tool_profiles"] as? [String] ?? [])
            if allowed.contains(id) {
                allowed.remove(id)
            } else {
                allowed.insert(id)
            }
            routingPolicy["allowed_tool_profiles"] = Array(allowed).sorted()
        }
    }

    func saveRouting(profileLimit: String, providerLimit: String) {
        guard let bridge, !routingBusy,
              let selected = savedProfiles.first(where: { $0["id"] as? String == routingProfile }) else { return }
        func parse(_ value: String) throws -> Int? {
            if value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                return nil
            }
            guard let limit = Int(value), (1 ... 1000).contains(limit) else {
                throw NSError(
                    domain: "Pot",
                    code: 1,
                    userInfo: [NSLocalizedDescriptionKey: "并发上限应为 1–1000，留空表示仅受全局上限约束。"]
                )
            }
            return limit
        }
        var update = routingPolicy
        do {
            var profiles = update["profile_limits"] as? [String: Int] ?? [:]
            var providers = update["provider_limits"] as? [String: Int] ?? [:]
            profiles[routingProfile] = try parse(profileLimit)
            providers[selected["provider"] as? String ?? ""] = try parse(providerLimit)
            update["profile_limits"] = profiles; update["provider_limits"] = providers
        } catch { result = error.localizedDescription; return }
        routingBusy = true
        Task {
            defer { routingBusy = false }
            do {
                routingPolicy = try await bridge.runtime.request("api/v1/agent/routing", body: update)
                result = "路由已保存；新任务使用新默认，已有任务保持固定。"
            } catch { result = "路由未保存：\(error.localizedDescription)；可刷新后重试。" }
        }
    }

    func saveProfile(name: String, model: String, copy: Bool) {
        guard let bridge, !routingBusy,
              var selected = savedProfiles.first(where: { $0["id"] as? String == routingProfile }) else { return }
        let expected = copy ? 0 : selected["revision"] as? Int ?? 0
        if copy {
            selected["id"] = "profile-" + UUID().uuidString.lowercased()
        }
        selected["name"] = name.trimmingCharacters(in: .whitespacesAndNewlines)
        selected["model"] = model.trimmingCharacters(in: .whitespacesAndNewlines)
        selected["revision"] = expected + 1
        routingBusy = true
        Task {
            do {
                _ = try await bridge.runtime.request("api/v1/agent", body: [
                    "profile": selected, "expected_revision": expected,
                    "request_id": UUID().uuidString, "follow_main": false,
                    "follow_worker": false,
                ])
                routingProfile = selected["id"] as? String ?? ""
                routingBusy = false; refreshRouting()
            } catch { routingBusy = false; result = "Profile 未保存：\(error.localizedDescription)" }
        }
    }

    func useProfileForOwner() {
        guard let bridge, !routingBusy, !routingProfile.isEmpty else { return }
        routingBusy = true
        let target = routingProfile
        let owner = routingOwner
        Task {
            defer { routingBusy = false }
            do {
                _ = try await bridge.runtime.request("api/v1/agent/migrate", body: [
                    "request_id": UUID().uuidString, "target_profile": target,
                    "owner": owner, "reason": "settings_changed",
                ])
                while true {
                    let response = try await bridge.runtime.request("api/v1/agent?owner=" + owner)
                    if response["pending_migration"] is NSNull || response["pending_migration"] == nil {
                        break
                    }
                    result = "任务正在等待交接，请在任务中结束当前轮次后继续；请勿重复提交。"
                    try await Task.sleep(nanoseconds: 750_000_000)
                }
                result = "此任务的 Agent 已切换；其他任务保持固定。"
                bridge.selectPage(3)
            } catch { result = "任务未切换；请检查连接、结束已有交接并刷新后重试。" }
        }
    }
}

@MainActor extension SettingsPanel {
    func saveProviderAllowance(tokens: String) {
        guard let bridge, !routingBusy,
              let provider = providerAllowance["provider"] as? String,
              let limit = UInt64(tokens), limit > 0, limit <= 9_007_199_254_740_991
        else {
            result = "请填写有效的正整数 Token 额度。"; return
        }
        let old = providerAllowance["policy"] as? [String: Any] ?? [:]
        let update: [String: Any] = ["provider": provider, "revision": providerAllowance["revision"] as? Int ?? 0,
                                     "policy": [
                                         "period": providerPeriod,
                                         "limit": limit,
                                         "timezone": old["timezone"] as? String ?? "Asia/Shanghai",
                                         "auto_resume": providerAutoResume,
                                     ]]
        routingBusy = true
        Task {
            defer { routingBusy = false }
            do {
                providerAllowance = try await bridge.runtime.request("api/v1/usage/providers", body: update)
                providerAllowance["enforcement"] = "pre_request"
                providerAllowances.removeAll { $0["provider"] as? String == provider }
                providerAllowances.append(providerAllowance)
                result = "服务商额度已保存；与全局额度同时生效，自动继续仅适用于支持检查点恢复的后端。"
            } catch { result = "额度未保存：\(error.localizedDescription)" }
        }
    }
}
