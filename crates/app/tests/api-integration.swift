import Foundation

@main struct APIIntegrationTests {
    static func main() {
        // The same models intentionally use different wire formats on Go versus Zen.
        let go = APIIntegration.openCodeRoute(kind: "opencode-go", model: "minimax-m3")!
        let zen = APIIntegration.openCodeRoute(kind: "opencode-zen", model: "minimax-m3")!
        assert(go.format == "anthropic_messages" && go.endpoint.hasSuffix("/go/v1/messages"))
        assert(zen.format == "openai_chat" && zen.endpoint.hasSuffix("/zen/v1/chat/completions"))
        assert(APIIntegration.openCodeRoute(kind: "opencode-go", model: "qwen3.8-max")!.format == "anthropic_messages")
        assert(APIIntegration.openCodeRoute(kind: "opencode-zen", model: "qwen3.8-max")!.format == "openai_chat")
        assert(APIIntegration.openCodeRoute(kind: "opencode-go", model: "claude-opus-4-6") == nil)
        assert(APIIntegration.openCodeRoute(kind: "opencode-zen", model: "gemini-3.1-pro")!.format == "gemini")
        assert(APIIntegration.openCodeRoute(kind: "opencode-zen", model: "gpt-future-unknown") == nil)
        assert(APIIntegration.openCodeRoute(kind: "custom", model: "minimax-m3") == nil)
        print("API integration route tests passed")
    }
}
