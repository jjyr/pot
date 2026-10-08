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
    @LocalizedMessage var checkMessage = ""
    var profile: [String: Any] = [:]
    var savedProfiles: [[String: Any]] = []
    var routingPolicy: [String: Any] = [:]
    var routingProfile = "", routingName = "", routingModel = ""
    var routingProfileLimit = "", routingProviderLimit = ""
    var routingBusy = false
    var routingOwner = "main", routingOwnerName = L.text("routing.main-conversation")
    var providerAllowances: [[String: Any]] = []
    var providerAllowance: [String: Any] = [:]
    var providerPeriod = "none", providerTokens = "2000000", providerAutoResume = true
    var apiEndpoint = "", apiModel = "", apiProtocol = "openai_chat", hasAPIKey = false
    var environment: [String: Any] = [:]
    var section = 0, revision = 0
    var awaitingBoot: String?
    var cpu = 4, memory = 8, disk = 32
    var key = "", limit = "10000", tokens = "8192", perTurn = "100", parallel = "8"
    var cpuField = "4", memoryField = "8", diskField = "32"
    @LocalizedMessage var result = ""

    var root: URL {
        bridge?.localRuntime?.root ?? URL(fileURLWithPath: NSHomeDirectory() + "/Library/Application Support/pot")
    }

    override init() {
        super.init(); changeAppearance(UserDefaults.standard.integer(forKey: "appearance"))
    }

    func changeAppearance(_ value: Int) {
        guard (0 ... 2).contains(value) else { return }
        UserDefaults.standard.set(value, forKey: "appearance")
        NSApp.appearance = value == 0 ? nil : NSAppearance(named: value == 1 ? .aqua : .darkAqua)
    }

    func refreshStatus() {
        guard let bridge else { return }
        if let old = awaitingBoot, let boot = bridge.snapshot["boot_id"] as? String, boot != old,
           bridge.snapshot["connected"] as? Bool == true
        {
            result = L.text("settingspanel.configuration-applied-environment-ready"); awaitingBoot = nil
        }
    }

    var publicState: [String: Any] {
        ["revision": revision, "section": section,
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
         "check_message": checkMessage,
         "profiles": savedProfiles, "routing": routingPolicy, "routing_profile": routingProfile,
         "routing_name": routingName, "routing_model": routingModel,
         "routing_profile_limit": routingProfileLimit, "routing_provider_limit": routingProviderLimit,
         "routing_owner": routingOwner, "routing_owner_name": routingOwnerName, "routing_busy": routingBusy,
         "provider_allowance": providerAllowance,
         "provider_period": providerPeriod, "provider_tokens": providerTokens,
         "provider_auto_resume": providerAutoResume,
         "runtime_ready": bridge?.snapshot["connected"] as? Bool ?? false,
         "environment_phase": environment["phase"] as? String ?? L.text("settingspanel.waiting-to-start"),
         "limit": limit, "tokens": tokens, "per_turn": perTurn, "parallel": parallel, "cpus": cpuField,
         "memory": memoryField, "disk": diskField,
         "selected_engine": profile["engine"] as? String ?? "pot", "selected_model": profile["model"] as? String ?? "",
         "api_endpoint": apiEndpoint, "api_model": apiModel, "api_protocol": apiProtocol, "has_api_key": hasAPIKey,
         "locale": L.locale, "appearance": UserDefaults.standard.integer(forKey: "appearance"), "result": result]
    }

    func refreshAI() {
        guard bridge?.snapshot["connected"] as? Bool == true else { return }
        Task {
            do { try await reloadAI() }
            catch { result = error.localizedDescription }
        }
    }

    func load() {
        key = ""; limit = "10000"; tokens = "8192"; perTurn = "100"; parallel = "8"
        if let data = try? Data(contentsOf: root.appendingPathComponent("product/execution-limits.json")),
           let limits = try? JSONSerialization.jsonObject(with: data) as? [String: Int]
        {
            limit = String(limits["request_limit"] ?? 10000); tokens = String(limits["max_tokens"] ?? 8192)
            perTurn = String(limits["max_requests_per_turn"] ?? 100); parallel =
                String(limits["max_parallel_tasks"] ?? 8)
        }
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
        ),
            let n = parsed(parallel, 1 ... 1000)
        else { result = L.text("settingspanel.check-the-request-limit-output-tokens-accept"); return }
        apply(
            ["request_limit": l, "max_tokens": t, "max_requests_per_turn": p, "max_parallel_tasks": n],
            to: root.appendingPathComponent("product/execution-limits.json")
        )
    }

    func apply(_ value: [String: Any], to path: URL) {
        guard let bridge, !bridge.running,
              !bridge.isApplying
        else { result = L.text("settingspanel.tasks-are-still-running-finish-them-before"); return }
        do {
            try FileManager.default.createDirectory(
                at: path.deletingLastPathComponent(),
                withIntermediateDirectories: true
            )
            let temp = path.deletingLastPathComponent().appendingPathComponent(".config-" + UUID().uuidString)
            defer { try? FileManager.default.removeItem(at: temp) }
            let data = try JSONSerialization.data(withJSONObject: value, options: .sortedKeys)
            guard FileManager.default.createFile(
                atPath: temp.path,
                contents: data,
                attributes: [.posixPermissions: 0o600]
            ) else { throw NSError(domain: "Pot", code: 1) }
            let handle = try FileHandle(forWritingTo: temp); try handle.synchronize(); try handle.close()
            guard rename(temp.path, path.path) == 0 else { throw NSError(domain: "Pot", code: 2) }
            load(); result = L.text("settingspanel.configuration-saved")
            if path.path.contains("/product/") {
                Task { await Task.detached { backendRestart() }.value }
            }
        } catch { result = L.text("settingspanel.save-failed-previous-configuration-kept") }
    }

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
                result = L.text("settingspanel.configuration-loaded-existing-tasks-keep-their-original")
            } catch { result = L.format(
                "settingspanel.unable-to-load-configuration-0",
                [String(describing: error.localizedDescription)]
            ) }
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
                    userInfo: [NSLocalizedDescriptionKey: L
                        .text("settingspanel.concurrency-must-be-1-1000-leave-blank")]
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
                result = L.text("settingspanel.routing-saved-new-tasks-use-the-new")
            } catch { result = L.format(
                "settingspanel.routing-not-saved-0-refresh-and-retry",
                [String(describing: error.localizedDescription)]
            ) }
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
            } catch { routingBusy = false; result = L.format(
                "settingspanel.profile-not-saved-0",
                [String(describing: error.localizedDescription)]
            ) }
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
                    result = L.text("settingspanel.this-task-is-waiting-for-handoff-finish")
                    try await Task.sleep(nanoseconds: 750_000_000)
                }
                result = L.text("settingspanel.this-task-s-agent-changed-other-tasks")
                bridge.selectPage(3)
            } catch { result = L.text("settingspanel.task-agent-unchanged-check-the-connection-finish") }
        }
    }
}

@MainActor extension SettingsPanel {
    func saveProviderAllowance(tokens: String) {
        guard let bridge, !routingBusy,
              let provider = providerAllowance["provider"] as? String,
              let limit = UInt64(tokens), limit > 0, limit <= 9_007_199_254_740_991
        else {
            result = L.text("settingspanel.enter-a-valid-positive-integer-token-allowance"); return
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
                result = L.text("settingspanel.provider-allowance-saved-it-applies-alongside-the")
            } catch { result = L.format(
                "settingspanel.allowance-not-saved-0",
                [String(describing: error.localizedDescription)]
            ) }
        }
    }
}
