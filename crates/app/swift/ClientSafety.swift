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

/// NSTextView offsets are UTF-16; preserve suffix text when inserting a reference.
enum ComposerReferenceQuery {
    static func range(in text: String, selection: NSRange) -> NSRange? {
        guard selection.length == 0, selection.location <= (text as NSString).length else { return nil }
        let prefix = (text as NSString).substring(to: selection.location)
        guard let word = prefix.split(whereSeparator: { $0.isWhitespace }).last,
              prefix.last?.isWhitespace == false, word.hasPrefix("/"),
              !word.dropFirst().contains("/") else { return nil }
        let length = (String(word) as NSString).length
        return NSRange(location: selection.location - length, length: length)
    }
}
