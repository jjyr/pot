import Foundation

enum HelperStatus {
    static func failure(_ process: Process) -> String {
        // terminationStatus throws an Objective-C exception if launch never succeeded.
        guard process.processIdentifier != 0 else { return "Helper did not start; reopen App to recover" }
        return "Helper exited (\(process.terminationStatus)); reopen App to recover"
    }
}

enum DraftReceipt {
    /// Main chat uses an empty editor key; a new task uses "new" until creation returns.
    static func apply(accepted: String, conversation: String, selected: String,
                      text: inout String, drafts: inout [String: String]) -> Bool
    {
        let key = conversation == "main" ? "" : conversation
        if drafts[key] == accepted {
            drafts[key] = ""
        }
        guard selected == key, text == accepted else { return false }
        text = ""
        drafts[key] = ""
        return true
    }
}
