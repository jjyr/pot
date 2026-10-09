import AppKit
import Foundation

@_silgen_name("pot_backend_request") private func backendRequest(
    _ path: UnsafePointer<CChar>,
    _ body: UnsafePointer<CChar>
) -> UnsafeMutablePointer<CChar>
@_silgen_name("pot_backend_free") private func backendFree(_ value: UnsafeMutablePointer<CChar>)
@_silgen_name("pot_backend_shutdown") func backendShutdown()
@_silgen_name("pot_backend_restart") func backendRestart()

/// Native adapters dispatch directly into the App-owned Rust backend, off the UI thread.
@MainActor final class RuntimeClient {
    var root: URL?
    var ip = ""
    func request(_ path: String, body: [String: Any]? = nil) async throws -> [String: Any] {
        let text = try body.map { try String(data: JSONSerialization.data(withJSONObject: $0), encoding: .utf8)! } ?? ""
        let data = await Task.detached {
            path.withCString { path in text.withCString { body in
                let result = backendRequest(path, body)
                defer { backendFree(result) }
                return Data(String(cString: result).utf8)
            } }
        }.value
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
