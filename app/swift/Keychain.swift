import Foundation
import Security

/// Generic-password records keep per-connection signing keys out of endpoint files and renderers.
@_cdecl("pot_keychain") public func potKeychain(
    _ account: UnsafePointer<CChar>, _ value: UnsafePointer<CChar>?
) -> UnsafeMutablePointer<CChar> {
    let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                kSecAttrService as String: "pot.server-signing-keys",
                                kSecAttrAccount as String: String(cString: account)]
    var reply: [String: Any] = [:]
    if let value {
        let text = String(cString: value)
        if text.isEmpty {
            let status = SecItemDelete(query as CFDictionary)
            reply = status == errSecSuccess || status == errSecItemNotFound ? ["value": ""] : ["error": status]
        } else {
            let attributes = [kSecValueData as String: Data(text.utf8)]
            var status = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
            if status == errSecItemNotFound {
                var item = query
                item.merge(attributes) { _, new in new }
                item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
                status = SecItemAdd(item as CFDictionary, nil)
            }
            reply = status == errSecSuccess ? ["value": ""] : ["error": status]
        }
    } else {
        var lookup = query
        lookup[kSecReturnData as String] = true
        lookup[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(lookup as CFDictionary, &result)
        if status == errSecSuccess, let data = result as? Data, let text = String(data: data, encoding: .utf8) {
            reply = ["value": text]
        } else {
            reply = ["error": status]
        }
    }
    let data = try! JSONSerialization.data(withJSONObject: reply)
    return strdup(String(data: data, encoding: .utf8)!)!
}
