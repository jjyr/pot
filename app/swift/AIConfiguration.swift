import AppKit
import Foundation

/// One configuration model shared by Chat setup and ordinary Settings.
@MainActor extension SettingsPanel {
    func openAI(fromChat: Bool = false) {
        section = 1; returnToChat = fromChat
        bridge?.selectPage(2); refreshAI()
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
        revision += 1; result = L.text("aiconfiguration.not-saved-yet-current-chat-configuration-kept")
    }

    func editConnection(_ id: String) {
        guard !aiBusy, let selected = savedProfiles.first(where: { $0["id"] as? String == id }) else { return }
        section = 1; profile = selected; connectionKind = selected["engine"] as? String ?? ""
        connectionName = selected["name"] as? String ?? ""
        connectionModel = selected["model"] as? String ?? ""
        secretRevision += 1; aiScreen = "manage"; checkMessage = ""; result = ""; modelOptions = []
        let connection = connections.first { $0["profile_id"] as? String == id }
        apiEndpoint = connection?["endpoint"] as? String ?? ""
        apiProtocol = connection?["protocol"] as? String ?? "openai_chat"
        hasAPIKey = connection?["has_key"] as? Bool ?? false
        if connectionKind == "pot" {
            let host = URLComponents(string: apiEndpoint)?.host ?? ""
            connectionKind = host == "api.anthropic.com" && apiProtocol == "anthropic_messages" ? "anthropic" :
                host == "generativelanguage.googleapis.com" && apiProtocol == "gemini" ? "gemini" :
                host == "api.openai.com" ? "openai" :
                host == "api.deepseek.com" ? "deepseek" :
                host == "opencode.ai" ? (apiEndpoint.contains("/zen/go/") ? "opencode-go" : "opencode-zen") : "custom"
        }
        revision += 1
    }

    func newConnection(_ kind: String) {
        guard !aiBusy else { return }
        connectionDraftID = "agent-" + UUID().uuidString.lowercased()
        secretRevision += 1; profile = [:]; connectionKind = kind; aiScreen = "manage"
        checkMessage = ""; result = ""; modelOptions = []; hasAPIKey = false
        switch kind {
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
            connectionName = L.text("ai.custom-api"); apiEndpoint = ""; apiProtocol = "openai_chat"
        default:
            connectionName = "OpenAI API"; apiEndpoint = "https://api.openai.com/v1"; apiProtocol = "openai_chat"
        }
        connectionModel = ""
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
            result = L.text("aiconfiguration.this-opencode-model-has-no-verified-request"); return false
        }
        apiEndpoint = route.endpoint; apiProtocol = route.format; return true
    }

    func checkAPI(endpoint: String, key: String) {
        guard let bridge, !aiBusy else { return }
        aiBusy = true; checkMessage = L.text("aiconfiguration.checking-connection-and-fetching-models-no-model")
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
                checkMessage = L.message(response["message"] as? String ?? L.text("aiconfiguration.check-complete"))
            } catch { modelOptions = []; checkMessage = error.localizedDescription }
        }
    }

    func saveConnection(name: String, model: String, endpoint: String, key: String) {
        guard let bridge, !aiBusy else { return }
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let model = model.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { result = L.text("aiconfiguration.enter-an-agent-name"); return }
        guard !model.isEmpty else { result = L.text("aiconfiguration.choose-or-enter-a-model"); return }
        let firstMain = activeSpec.isEmpty && routingOwner == "main"
        if connectionKind.hasPrefix("opencode-"), !selectOpenCodeRoute(model) {
            return
        }
        if endpoint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            result = L.text("aiconfiguration.enter-a-service-url"); return
        }
        let expected = profile["revision"] as? Int ?? 0
        if connectionDraftID.isEmpty {
            connectionDraftID = "agent-" + UUID().uuidString.lowercased()
        }
        let target = profile["id"] as? String ?? connectionDraftID
        let kind = connectionKind, protocolName = apiProtocol
        aiBusy = true; result = firstMain ? L.text("aiconfiguration.saving-and-setting-the-main-agent") : L
            .text("aiconfiguration.saving-connection")
        Task {
            defer { aiBusy = false }
            do {
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
                try await reloadAI()
                // Keep the saved identity if activation later needs a retry.
                profile = savedProfiles.first { $0["id"] as? String == target } ?? [:]
                if firstMain {
                    while true {
                        let state = try await bridge.runtime.request("api/v1/agent")
                        if state["pending_migration"] is NSNull || state["pending_migration"] == nil {
                            break
                        }
                        result = L.text("aiconfiguration.setting-the-main-agent-waiting-for-the")
                        try await Task.sleep(nanoseconds: 750_000_000)
                    }
                    try await reloadAI()
                    guard activeSpec["source_profile_id"] as? String == target else {
                        result = L.text("aiconfiguration.connection-saved-but-the-main-agent-is"); return
                    }
                    draftProfile = target; draftModel = model; draftRevision += 1
                }
                aiScreen = "main"; secretRevision += 1
                if firstMain, returnToChat {
                    bridge.selectPage(0); returnToChat = false
                }
                if routingOwner != "main" {
                    routingProfile = target; editRoutingProfile(target); section = 4
                }
                revision += 1; result = L.text("aiconfiguration.connection-saved-no-model-requests-sent")
            } catch {
                // A lost response may follow a successful save. Reconcile before retrying.
                try? await reloadAI()
                if let saved = savedProfiles.first(where: { $0["id"] as? String == target }) {
                    profile = saved
                }
                result = L.text("aiconfiguration.save-incomplete-refresh-and-retry") + error.localizedDescription
            }
        }
    }

    func saveMain(model: String) {
        guard let bridge, !aiBusy,
              var selected = savedProfiles.first(where: { $0["id"] as? String == draftProfile })
        else { result = L.text("aiconfiguration.add-and-choose-an-agent-first"); return }
        let model = model.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !model.isEmpty else { result = L.text("aiconfiguration.enter-a-model-for-chat"); return }
        if activeSpec["source_profile_id"] as? String == draftProfile, activeSpec["model"] as? String == model,
           activeSpec["source_profile_revision"] as? Int == selected["revision"] as? Int
        {
            result = L.text("aiconfiguration.current-configuration-is-active"); aiScreen = "main"; if returnToChat {
                bridge.selectPage(0); returnToChat = false
            }; return
        }
        let expected = selected["revision"] as? Int ?? 0
        selected["revision"] = expected + 1; selected["model"] = model
        aiBusy = true; result = L.text("aiconfiguration.saving-chat-configuration")
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
                    result = L.text("aiconfiguration.switching-agent-waiting-for-the-current-turn")
                    try await Task.sleep(nanoseconds: 750_000_000)
                }
                try await reloadAI()
                guard activeSpec["source_profile_id"] as? String == draftProfile,
                      activeSpec["model"] as? String == model
                else {
                    result = L.text("aiconfiguration.switch-not-applied-yet-refresh-status-and"); return
                }
                draftModel = model; aiScreen = "main"; revision += 1; result = L
                    .text("aiconfiguration.main-agent-is-active-existing-background-tasks")
                if returnToChat {
                    bridge.selectPage(0); returnToChat = false
                }
            } catch {
                result = error.localizedDescription.contains("migration is pending") ? L
                    .text("aiconfiguration.a-switch-is-already-pending-wait-for") : L
                    .text("aiconfiguration.chat-configuration-unchanged-check-the-connection-refresh")
            }
        }
    }
}
