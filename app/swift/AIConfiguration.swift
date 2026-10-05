import AppKit
import Foundation

/// One configuration model shared by Chat setup and ordinary Settings.
@MainActor extension SettingsPanel {
    var activeNeedsLogin: Bool {
        let engine = activeSpec["engine"] as? String ?? ""
        return ["codex", "claude", "agy"].contains(engine) && agentStates
            .contains { $0["id"] as? String == engine && $0["authentication"] as? String == "required" }
    }

    func openAI(fromChat: Bool = false) {
        setup = false; section = 1; returnToChat = fromChat
        bridge?.selectPage(2); refreshAgents()
    }

    func reloadAI() async throws {
        guard let bridge else { return }
        let state = try await bridge.terminal.request("api/v1/agent")
        savedProfiles = state["profiles"] as? [[String: Any]] ?? []
        let bindings = state["bindings"] as? [[String: Any]] ?? []
        activeSpec = bindings
            .first { $0["pot_owner"] as? String == "main" && $0["state"] as? String == "active"
            }?["spec"] as? [String: Any] ?? [:]
        let providerState = try await bridge.terminal.request("api/v1/providers")
        connections = providerState["connections"] as? [[String: Any]] ?? []
        if !draftInitialized {
            draftProfile = activeSpec["source_profile_id"] as? String ?? ""
            draftModel = activeSpec["model"] as? String ?? ""
            draftInitialized = true; revision += 1
        }
    }

    func chooseAI(_ id: String) {
        guard !aiBusy else { return }
        draftProfile = id; draftRevision += 1
        draftModel = savedProfiles.first { $0["id"] as? String == id }?["model"] as? String ?? ""
        revision += 1; result = "尚未保存；当前聊天配置保持不变。"
    }

    func editConnection(_ id: String) {
        guard !aiBusy, let selected = savedProfiles.first(where: { $0["id"] as? String == id }) else { return }
        section = 1; profile = selected; connectionKind = selected["engine"] as? String ?? ""
        connectionName = selected["name"] as? String ?? ""
        connectionModel = selected["model"] as? String ?? ""
        secretRevision += 1; aiScreen = "manage"; checkMessage = ""; modelOptions = []
        let connection = connections.first { $0["profile_id"] as? String == id }
        apiEndpoint = connection?["endpoint"] as? String ?? ""
        apiProtocol = connection?["protocol"] as? String ?? "openai_chat"
        hasAPIKey = connection?["has_key"] as? Bool ?? false
        let launch = selected["launch_spec"] as? [String: Any] ?? [:]
        invocationExecutable = launch["executable"] as? String ?? connectionKind
        let literals = (launch["argv"] as? [[String: Any]] ?? [])
            .compactMap { $0["kind"] as? String == "literal" ? $0["value"] as? String : nil }
        invocationArgs = String(
            data: (try? JSONSerialization.data(withJSONObject: literals)) ?? Data("[]".utf8),
            encoding: .utf8
        ) ?? "[]"
        revision += 1
    }

    func newConnection(_ kind: String) {
        guard !aiBusy else { return }
        secretRevision += 1; profile = [:]; connectionKind = kind; aiScreen = "manage"
        checkMessage = ""; modelOptions = []; hasAPIKey = false
        switch kind {
        case "deepseek": connectionName = "DeepSeek"; apiEndpoint =
            "https://api.deepseek.com/chat/completions"; apiProtocol = "openai_chat"
        case "openai": connectionName = "OpenAI"; apiEndpoint = "https://api.openai.com/v1/responses"; apiProtocol = "openai_responses"
        case "anthropic": connectionName = "Anthropic"; apiEndpoint =
            "https://api.anthropic.com/v1/messages"; apiProtocol = "anthropic_messages"
        case "gemini": connectionName = "Gemini"; apiEndpoint =
            "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent"; apiProtocol = "gemini"
        case "custom": connectionName = "自定义 Agent"; apiEndpoint = ""; apiProtocol = "openai_chat"
        default: connectionName = kind == "agy" ? "AGY" : kind.capitalized; apiEndpoint = ""
        }
        connectionModel = ""; revision += 1
    }

    func checkAPI(endpoint: String, key: String) {
        guard let bridge, !aiBusy else { return }
        aiBusy = true; checkMessage = "正在检查连接并读取模型列表；不会发送模型请求…"
        Task {
            defer { aiBusy = false }
            do {
                let response = try await bridge.terminal.request("api/v1/providers/check", body: [
                    "endpoint": endpoint.trimmingCharacters(in: .whitespacesAndNewlines), "api_key": key,
                    "protocol": apiProtocol, "profile_id": profile["id"] as? String ?? "",
                ])
                modelOptions = response["models"] as? [String] ?? []
                checkMessage = response["message"] as? String ?? "检查完成。"
            } catch { modelOptions = []; checkMessage = error.localizedDescription }
        }
    }

    func saveConnection(name: String, model: String, endpoint: String, key: String) {
        guard let bridge, !aiBusy else { return }
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let model = model.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !model.isEmpty else { result = "请填写 Agent 名称和模型标识。"; return }
        let native = ["codex", "claude", "agy"].contains(connectionKind)
        if native, !setupCanStart(connectionKind) {
            result = "请先完成安装和登录，再检查状态。"; return
        }
        if !native, endpoint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            result = "请填写服务地址。"; return
        }
        let expected = profile["revision"] as? Int ?? 0
        let target = profile["id"] as? String ?? "agent-" + UUID().uuidString.lowercased()
        let kind = connectionKind, protocolName = apiProtocol
        aiBusy = true; result = "正在保存连接；当前聊天配置保持不变…"
        Task {
            defer { aiBusy = false }
            do {
                if native {
                    var value = profile
                    value["id"] = target; value["revision"] = expected + 1; value["name"] = name
                    value["engine"] = kind; value["model"] = model
                    value["provider"] = kind == "claude" ? "anthropic" : kind == "agy" ? "google" : "openai"
                    value["auth_ref"] = "cli:" + kind
                    if value["params"] == nil {
                        value["params"] = ["max_output_tokens": 8192]
                    }
                    if value["tool_policy"] == nil {
                        value["tool_policy"] = [
                            "worker_orchestration": false,
                            "summary_read_only": true,
                            "unattended": "guest_vm",
                        ]
                    }
                    _ = try await bridge.terminal.request(
                        "api/v1/agent",
                        body: [
                            "profile": value,
                            "expected_revision": expected,
                            "request_id": UUID().uuidString,
                            "follow_main": false,
                            "follow_worker": false,
                        ]
                    )
                } else {
                    _ = try await bridge.terminal.request(
                        "api/v1/providers",
                        body: [
                            "profile_id": target,
                            "name": name,
                            "expected_revision": expected,
                            "request_id": UUID().uuidString,
                            "endpoint": endpoint.trimmingCharacters(in: .whitespacesAndNewlines),
                            "api_key": key,
                            "model": model,
                            "protocol": protocolName,
                            "follow_main": false,
                        ]
                    )
                }
                try await reloadAI()
                aiScreen = "main"; secretRevision += 1
                if returnToChat, activeSpec.isEmpty {
                    draftProfile = target; draftModel = model; draftRevision += 1
                }
                if routingOwner != "main" {
                    routingProfile = target; editRoutingProfile(target); section = 4
                }
                revision += 1; result = "连接已保存。请在上方确认聊天 Agent 和模型后保存；未发送模型请求。"
            } catch { result = "连接未保存：" + error.localizedDescription }
        }
    }

    func saveMain(model: String) {
        guard let bridge, !aiBusy,
              var selected = savedProfiles.first(where: { $0["id"] as? String == draftProfile })
        else { result = "请先添加并选择一个 Agent。"; return }
        let model = model.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !model.isEmpty else { result = "请填写聊天使用的模型。"; return }
        let engine = selected["engine"] as? String ?? ""
        if engine != "pi", !setupCanStart(engine) {
            result = "此 Agent 需要安装或登录，请完成后重新检查。"; return
        }
        if activeSpec["source_profile_id"] as? String == draftProfile, activeSpec["model"] as? String == model,
           activeSpec["source_profile_revision"] as? Int == selected["revision"] as? Int
        {
            result = "当前配置已生效。"; if returnToChat {
                finishSetup(); returnToChat = false
            }; return
        }
        let expected = selected["revision"] as? Int ?? 0
        selected["revision"] = expected + 1; selected["model"] = model
        aiBusy = true; result = "正在保存聊天配置…"
        Task {
            defer { aiBusy = false }
            do {
                _ = try await bridge.terminal.request(
                    "api/v1/agent",
                    body: [
                        "profile": selected,
                        "expected_revision": expected,
                        "request_id": UUID().uuidString,
                        "follow_main": true,
                        "follow_worker": false,
                    ]
                )
                while true {
                    let state = try await bridge.terminal.request("api/v1/agent")
                    if state["pending_migration"] is NSNull || state["pending_migration"] == nil {
                        break
                    }
                    result = "正在切换 Agent，等待当前轮次或暂停任务结束。请在原会话停止或继续后等待交接；请勿重复提交。"
                    try await Task.sleep(nanoseconds: 750_000_000)
                }
                try await reloadAI()
                guard activeSpec["source_profile_id"] as? String == draftProfile,
                      activeSpec["model"] as? String == model
                else {
                    result = "切换尚未生效，请刷新状态后重试。"; return
                }
                draftModel = model; revision += 1; result = "聊天配置已生效。已有后台任务保持原配置。"
                if returnToChat {
                    finishSetup(); returnToChat = false
                }
            } catch {
                result = error.localizedDescription.contains("migration is pending") ? "已有切换正在等待，请等待当前会话结束后再试。" : "聊天配置未切换，请检查连接并刷新后重试。"
            }
        }
    }

    func openLogin(_ engine: String) {
        guard ["codex", "claude", "agy"].contains(engine), !aiBusy else { return }
        loginEngine = engine; loginVisible = true; loginMessage = "在下方粘贴登录命令并按 Enter，按终端提示在浏览器完成授权。"
        bridge?.selectPage(7); refreshAgents()
    }

    var loginCommand: String {
        switch loginEngine {
        case "codex": "codex login --device-auth"
        case "claude": "claude auth login"
        default: "agy --help"
        }
    }

    func continueLogin() {
        let status = agentStates.first { $0["id"] as? String == loginEngine }
        let authenticated = status?["authentication"] as? String == "authenticated"
        guard authenticated || setupCanStart(loginEngine) else { loginMessage = "请先完成登录，或重新检查状态。"; return }
        loginVisible = false; section = 1; bridge?.selectPage(2)
        result = authenticated ? "登录完成，请确认模型并保存。" : "登录状态未知，请根据终端结果确认可用后填写模型。"
    }
}
