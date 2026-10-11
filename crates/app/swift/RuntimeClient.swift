import AppKit
import Foundation

@_silgen_name("pot_backend_request") private func backendRequest(
    _ path: UnsafePointer<CChar>,
    _ body: UnsafePointer<CChar>
) -> UnsafeMutablePointer<CChar>
@_silgen_name("pot_backend_free") private func backendFree(_ value: UnsafeMutablePointer<CChar>)
@_silgen_name("pot_backend_shutdown") func backendShutdown()
@_silgen_name("pot_backend_restart") func backendRestart()
@_silgen_name("pot_backend_remote_mode") func backendRemoteMode() -> Bool
@_silgen_name("pot_backend_mode_generation") func backendModeGeneration() -> UInt64

/// Native adapters dispatch directly into the App-owned Rust backend, off the UI thread.
@MainActor final class RuntimeClient {
    var root: URL?
    var ip = ""
    var assistantID = "main"
    var remoteMode = backendRemoteMode()
    func scopedCopy() -> RuntimeClient {
        let copy = RuntimeClient(); copy.root = root; copy.assistantID = assistantID; copy
            .remoteMode = remoteMode; return copy
    }

    func request(_ path: String, body: [String: Any]? = nil) async throws -> [String: Any] {
        let path = path.hasPrefix("api/v1/") && !path.hasPrefix("api/v1/assistants") && !path
            .hasPrefix("api/v1/assistant-tasks") && path != "api/v1/runtime" && !path.hasPrefix("api/v1/runtime/")
            ? "api/v1/assistants/" + assistantID + "/" + String(path.dropFirst(7)) : path
        let generation = backendModeGeneration()
        let requestPath = "_client/" + (remoteMode ? "remote/" : "local/") + path
        let text = try body.map { try String(data: JSONSerialization.data(withJSONObject: $0), encoding: .utf8)! } ?? ""
        let data = await Task.detached {
            requestPath.withCString { path in text.withCString { body in
                let result = backendRequest(path, body)
                defer { backendFree(result) }
                return Data(String(cString: result).utf8)
            } }
        }.value
        guard generation == backendModeGeneration() else { throw CancellationError() }
        let reply = try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
        if let error = reply["error"] as? String {
            throw NSError(
                domain: "PotRuntime",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: String(L.message(error).prefix(1024))]
            )
        }
        return reply["value"] as? [String: Any] ?? [:]
    }
}

@_cdecl("pot_bridge_startup_error") public func startupError(_ message: UnsafePointer<CChar>) {
    MainActor.assumeIsolated {
        NSApplication.shared.activate(ignoringOtherApps: true)
        let alert = NSAlert(); alert.alertStyle = .critical; alert.messageText = "Pot"
        alert.informativeText = L.message(String(cString: message)); alert.runModal()
    }
}
