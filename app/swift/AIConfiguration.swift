import AppKit
import Foundation

/// One configuration model shared by Chat setup and ordinary Settings.
@MainActor extension SettingsPanel {
    var activeNeedsLogin: Bool {
        let engine = activeSpec["engine"] as? String ?? ""
        return ["codex", "grok", "claude", "agy"].contains(engine) && agentStates
            .contains { $0["id"] as? String == engine && $0["authentication"] as? String == "required" }
    }

    func openAI(fromChat: Bool = false) {
        setup = false; section = 1; returnToChat = fromChat
        bridge?.selectPage(2); refreshAgents()
    }

    func reloadAI() async throws {
        guard let bridge else { return }
        let state = try await bridge.runtime.request("api/v1/agent")
        savedProfiles = state["profiles"] as? [[String: Any]] ?? []
        let bindings = state["bindings"] as? [[String: Any]] ?? []
        activeSpec = bindings
            .first { $0["pot_owner"] as? String == "main" && $0["state"] as? String == "active"
            }?["spec"] as? [String: Any] ?? [:]
        let providerState = try await bridge.runtime.request("api/v1/providers")
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
        if loginVisible {
            cancelLogin()
        }
        guard !aiBusy, let selected = savedProfiles.first(where: { $0["id"] as? String == id }) else { return }
        section = 1; profile = selected; connectionKind = selected["engine"] as? String ?? ""
        connectionName = selected["name"] as? String ?? ""
        connectionModel = selected["model"] as? String ?? ""
        secretRevision += 1; aiScreen = "manage"; checkMessage = ""; result = ""; modelOptions = []
        let connection = connections.first { $0["profile_id"] as? String == id }
        apiEndpoint = connection?["endpoint"] as? String ?? ""
        apiProtocol = connection?["protocol"] as? String ?? "openai_chat"
        hasAPIKey = connection?["has_key"] as? Bool ?? false
        if connectionKind == "pi" {
            let host = URLComponents(string: apiEndpoint)?.host ?? ""
            connectionKind = host == "api.anthropic.com" && apiProtocol == "anthropic_messages" ? "anthropic" :
                host == "generativelanguage.googleapis.com" && apiProtocol == "gemini" ? "gemini" :
                host == "api.openai.com" ? "openai" :
                host == "api.deepseek.com" ? "deepseek" :
                host == "opencode.ai" ? (apiEndpoint.contains("/zen/go/") ? "opencode-go" : "opencode-zen") : "custom"
        }
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
        if loginVisible {
            cancelLogin()
        }
        connectionDraftID = "agent-" + UUID().uuidString.lowercased()
        secretRevision += 1; profile = [:]; connectionKind = kind; aiScreen = "manage"
        checkMessage = ""; result = ""; modelOptions = []; hasAPIKey = false
        switch kind {
        case "codex", "grok":
            connectionName = kind.capitalized; apiEndpoint = ""
        case "deepseek":
            connectionName = "DeepSeek"; apiEndpoint = "https://api.deepseek.com/v1/chat/completions"; apiProtocol = "openai_chat"
        case "opencode-go", "opencode-zen":
            connectionName = kind == "opencode-go" ? "OpenCode Go" : "OpenCode Zen"
            apiEndpoint = kind == "opencode-go" ? "https://opencode.ai/zen/go/v1/chat/completions" : "https://opencode.ai/zen/v1/chat/completions"
            apiProtocol = "openai_chat"
        case "anthropic":
            connectionName = "Claude"; apiEndpoint = "https://api.anthropic.com/v1/messages"; apiProtocol = "anthropic_messages"
        case "gemini":
            connectionName = "Gemini"; apiEndpoint =
                "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent"; apiProtocol = "gemini"
        case "custom":
            connectionName = "自定义 API"; apiEndpoint = ""; apiProtocol = "openai_chat"
        default:
            connectionName = "OpenAI API"; apiEndpoint = "https://api.openai.com/v1"; apiProtocol = "openai_chat"
        }
        connectionModel = ""
        if ["codex", "grok"].contains(kind),
           let agent = agentStates
           .first(where: { $0["id"] as? String == kind && $0["authentication"] as? String == "authenticated" })
        {
            modelOptions = agent["models"] as? [String] ?? []
            connectionModel = modelOptions.first ?? ""
        }
        revision += 1
    }

    /// Accept a compatible base URL as well as an existing full request endpoint.
    func connectionEndpoint(_ value: String) -> String {
        let value = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard var parts = URLComponents(string: value), parts.host != nil,
              parts.query == nil, parts.fragment == nil else { return value }
        let path = parts.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        if path.hasSuffix("chat/completions") || path.hasSuffix("responses") || path.hasSuffix("messages") || path
            .contains("/models/")
        {
            return value
        }
        guard path.isEmpty || path == "v1" || path == "v1beta" || path.hasSuffix("/v1") else { return value }
        let base = path.isEmpty ? (apiProtocol == "gemini" ? "/v1beta" : "/v1") : "/" + path
        let suffix = apiProtocol == "openai_responses" ? "/responses" : apiProtocol == "anthropic_messages" ?
            "/messages" : apiProtocol == "gemini" ? "/models/{model}:streamGenerateContent" : "/chat/completions"
        parts.path = base + suffix
        return parts.string ?? value
    }

    func selectOpenCodeRoute(_ model: String) -> Bool {
        guard let route = APIIntegration.openCodeRoute(kind: connectionKind, model: model) else {
            result = "此 OpenCode 模型尚无已验证的请求格式，请使用受支持模型或自定义 API。"; return false
        }
        apiEndpoint = route.endpoint; apiProtocol = route.format; return true
    }

    func checkAPI(endpoint: String, key: String) {
        guard let bridge, !aiBusy else { return }
        aiBusy = true; checkMessage = "正在检查连接并读取模型列表；不会发送模型请求…"
        Task {
            defer { aiBusy = false }
            do {
                let response = try await bridge.runtime.request("api/v1/providers/check", body: [
                    "endpoint": connectionEndpoint(endpoint), "api_key": key,
                    "protocol": apiProtocol, "profile_id": profile["id"] as? String ?? "",
                ])
                modelOptions = response["models"] as? [String] ?? []
                if connectionKind
                    .hasPrefix("opencode-")
                {
                    modelOptions = modelOptions
                        .filter { APIIntegration.openCodeRoute(kind: connectionKind, model: $0) != nil }
                }
                checkMessage = response["message"] as? String ?? "检查完成。"
            } catch { modelOptions = []; checkMessage = error.localizedDescription }
        }
    }

    func saveConnection(name: String, model: String, endpoint: String, key: String) {
        guard let bridge, !aiBusy else { return }
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let model = model.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { result = "请填写 Agent 名称。"; return }
        guard !model.isEmpty else { result = "请选择或填写模型。"; return }
        let native = ["codex", "grok", "claude", "agy"].contains(connectionKind)
        let firstMain = activeSpec.isEmpty && routingOwner == "main"
        if connectionKind.hasPrefix("opencode-"), !selectOpenCodeRoute(model) {
            return
        }
        if native, !setupCanStart(connectionKind) {
            result = "请先完成安装和登录，再检查状态。"; return
        }
        if !native, endpoint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            result = "请填写服务地址。"; return
        }
        let expected = profile["revision"] as? Int ?? 0
        if connectionDraftID.isEmpty {
            connectionDraftID = "agent-" + UUID().uuidString.lowercased()
        }
        let target = profile["id"] as? String ?? connectionDraftID
        let kind = connectionKind, protocolName = apiProtocol
        aiBusy = true; result = firstMain ? "正在保存并设置主 Agent…" : "正在保存连接…"
        Task {
            defer { aiBusy = false }
            do {
                if native {
                    var value = profile
                    value["id"] = target; value["revision"] = expected + 1; value["name"] = name
                    value["engine"] = kind; value["model"] = model
                    value["provider"] = kind == "grok" ? "xai" : kind == "claude" ? "anthropic" : kind == "agy" ?
                        "google" : "openai"
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
                    _ = try await bridge.runtime.request(
                        "api/v1/agent",
                        body: [
                            "profile": value,
                            "expected_revision": expected,
                            "request_id": UUID().uuidString,
                            "follow_main": firstMain,
                            "follow_worker": false,
                        ]
                    )
                } else {
                    _ = try await bridge.runtime.request(
                        "api/v1/providers",
                        body: [
                            "profile_id": target,
                            "name": name,
                            "expected_revision": expected,
                            "request_id": UUID().uuidString,
                            "endpoint": connectionEndpoint(kind.hasPrefix("opencode-") ? apiEndpoint : endpoint),
                            "api_key": key,
                            "model": model,
                            "protocol": protocolName,
                            "follow_main": firstMain,
                        ]
                    )
                }
                try await reloadAI()
                // Keep the saved identity if activation later needs a retry.
                profile = savedProfiles.first { $0["id"] as? String == target } ?? [:]
                if firstMain {
                    while true {
                        let state = try await bridge.runtime.request("api/v1/agent")
                        if state["pending_migration"] is NSNull || state["pending_migration"] == nil {
                            break
                        }
                        result = "正在设置主 Agent，请等待当前会话交接…"
                        try await Task.sleep(nanoseconds: 750_000_000)
                    }
                    try await reloadAI()
                    guard activeSpec["source_profile_id"] as? String == target else {
                        result = "连接已保存，主 Agent 尚未生效；请刷新后在主 Agent 中完成设置。"; return
                    }
                    draftProfile = target; draftModel = model; draftRevision += 1
                }
                aiScreen = "main"; secretRevision += 1
                if firstMain, returnToChat {
                    finishSetup(); returnToChat = false
                }
                if routingOwner != "main" {
                    routingProfile = target; editRoutingProfile(target); section = 4
                }
                revision += 1; result = "连接已保存；未发送模型请求。"
            } catch {
                // A lost response may follow a successful save. Reconcile before retrying.
                try? await reloadAI()
                if let saved = savedProfiles.first(where: { $0["id"] as? String == target }) {
                    profile = saved
                }
                result = "保存未完成，请刷新后重试：" + error.localizedDescription
            }
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
            result = "当前配置已生效。"; aiScreen = "main"; if returnToChat {
                finishSetup(); returnToChat = false
            }; return
        }
        let expected = selected["revision"] as? Int ?? 0
        selected["revision"] = expected + 1; selected["model"] = model
        aiBusy = true; result = "正在保存聊天配置…"
        Task {
            defer { aiBusy = false }
            do {
                _ = try await bridge.runtime.request(
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
                    let state = try await bridge.runtime.request("api/v1/agent")
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
                draftModel = model; aiScreen = "main"; revision += 1; result = "主 Agent 已生效。已有后台任务保持原配置。"
                if returnToChat {
                    finishSetup(); returnToChat = false
                }
            } catch {
                result = error.localizedDescription.contains("migration is pending") ? "已有切换正在等待，请等待当前会话结束后再试。" : "聊天配置未切换，请检查连接并刷新后重试。"
            }
        }
    }

    func openLogin(_ engine: String) {
        startAuthorization(engine)
    }

    func cancelLogin() {
        stopAuthorization()
    }

    func reopenLogin() {
        if let url = URL(string: authorizationURL), loginVisible {
            NSWorkspace.shared.open(url)
        }
    }
}
