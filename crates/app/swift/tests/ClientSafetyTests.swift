import Foundation

@main enum ClientSafetyTests {
    static func main() throws {
        let query = "前😀 /desk 后"
        let caret = ("前😀 /desk" as NSString).length
        let range = ComposerReferenceQuery.range(in: query, selection: NSRange(location: caret, length: 0))!
        assert((query as NSString).substring(with: range) == "/desk")
        assert((query as NSString).replacingCharacters(in: range, with: "/tag:desktop ") == "前😀 /tag:desktop  后")
        for value in ["/tmp/file", "https://host", "plain", "/ ", ""] {
            assert(ComposerReferenceQuery.range(
                in: value,
                selection: NSRange(location: (value as NSString).length, length: 0)
            ) == nil)
        }
        assert(ComposerReferenceQuery.range(in: "/", selection: NSRange(location: 1, length: 0)) == NSRange(
            location: 0,
            length: 1
        ))
        assert(ComposerReferenceQuery.range(in: "/tag", selection: NSRange(location: 1, length: 2)) == nil)
        let neverLaunched = Process()
        assert(HelperStatus.failure(neverLaunched).contains("did not start"))
        let exited = Process()
        exited.executableURL = URL(fileURLWithPath: "/usr/bin/true")
        try exited.run()
        exited.waitUntilExit()
        assert(HelperStatus.failure(exited).contains("exited (0)"))

        // A late receipt from A must not erase B's identical editor text.
        var text = "same text"
        var drafts = ["a": "same text", "b": "same text"]
        assert(!DraftReceipt.apply(accepted: "same text", conversation: "a", selected: "b",
                                   text: &text, drafts: &drafts))
        assert(text == "same text" && drafts["b"] == "same text" && drafts["a"] == "")

        // Returning to A must preserve edits made after sending.
        text = "edited"
        drafts["a"] = "edited"
        assert(!DraftReceipt.apply(accepted: "same text", conversation: "a", selected: "a",
                                   text: &text, drafts: &drafts))
        assert(text == "edited" && drafts["a"] == "edited")

        for (conversation, selected) in [("main", ""), ("a", "a"), ("new", "new")] {
            text = "sent"
            drafts[selected] = "sent"
            assert(DraftReceipt.apply(accepted: "sent", conversation: conversation, selected: selected,
                                      text: &text, drafts: &drafts))
            assert(text.isEmpty && drafts[selected] == "")
        }
        print("Client safety regressions passed")
    }
}
