import AppKit
import Foundation

/// Native preferences and local Runtime restart service. Product settings state lives in pot-ui.
@MainActor final class SettingsPanel: NSObject {
    weak var bridge: PotBridge?
    var runtimeStatus: [String: Any] = [:]
    var revision = 0
    var awaitingBoot: String?
    var limit = "10000", tokens = "8192", perTurn = "100", parallel = "8"
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
        wakeNativeUI()
    }

    func refreshStatus() {
        if let old = awaitingBoot, let boot = bridge?.snapshot["boot_id"] as? String, boot != old,
           bridge?.snapshot["connected"] as? Bool == true
        {
            result = L.text("settingspanel.configuration-applied-runtime-ready"); awaitingBoot = nil
        }
    }

    var publicState: [String: Any] {
        ["revision": revision, "runtime_ready": bridge?.snapshot["connected"] as? Bool ?? false,
         "runtime_phase": runtimeStatus["phase"] as? String ?? "", "limit": limit, "tokens": tokens,
         "per_turn": perTurn,
         "parallel": parallel, "locale": L.locale, "appearance": UserDefaults.standard.integer(forKey: "appearance"),
         "result": result]
    }

    func load() {
        limit = "10000"; tokens = "8192"; perTurn = "100"; parallel = "8"
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
}
