import Foundation

/// Native Runtime requests, independent of any renderer.
@MainActor final class RuntimeClient {
    var root: URL?
    var ip = ""
    private func credentials() throws -> (URL, String, String) {
        guard let root else { throw URLError(.notConnectedToInternet) }
        let data = try Data(contentsOf: root.appendingPathComponent("product/runtime.json"))
        guard let endpoint = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let port = endpoint["port"] as? Int, let token = endpoint["token"] as? String,
              let boot = endpoint["boot_id"] as? String,
              let url = URL(string: "http://127.0.0.1:\(port)") else { throw URLError(.badServerResponse) }
        return (url, token, boot)
    }

    func request(_ path: String, body: [String: Any]? = nil) async throws -> [String: Any] {
        let (base, token, _) = try credentials()
        let pieces = path.split(separator: "?", maxSplits: 1)
        var parts = URLComponents(url: base.appendingPathComponent(String(pieces[0])), resolvingAgainstBaseURL: false)!
        if pieces.count > 1 {
            parts.percentEncodedQuery = String(pieces[1])
        }
        var request = URLRequest(url: parts.url!)
        request.httpMethod = body == nil ? "GET" : "POST"; request
            .timeoutInterval = path == "api/v1/authorization/start" ? 70 :
            (path == "api/v1/sandboxes" && body != nil ? 120 : 20)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let body {
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, response) = try await URLSession.shared.data(for: request)
        let value = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        guard let response = response as? HTTPURLResponse, response.statusCode == 200 else {
            throw NSError(
                domain: "PotRuntime",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: String((value["message"] as? String ?? "Runtime 请求失败")
                        .prefix(1024))]
            )
        }
        return value
    }
}
