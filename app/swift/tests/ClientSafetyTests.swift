import Foundation

@main enum ClientSafetyTests {
    static func main() throws {
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
