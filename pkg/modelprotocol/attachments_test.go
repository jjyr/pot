package modelprotocol

import (
	"encoding/base64"
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"strings"
	"testing"
)

func TestNativeUserAttachmentsRemainOnTheirMessage(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\n%%EOF"))
	for _, p := range []Protocol{OpenAIChat, OpenAIResponses, AnthropicMessages, Gemini} {
		for _, text := range []string{"inspect these", ""} {
			t.Run(string(p)+"/"+text, func(t *testing.T) {
				msg := TextMessage("user", text)
				msg.Images = []ModelImage{{MIMEType: "image/png", Data: "AQID", Width: 2, Height: 3}}
				msg.Documents = []ModelDocument{{Name: "report.pdf", Data: pdf}}
				body, err := Encode(Request{Protocol: p, Model: "test", System: "system", Messages: []Message{msg, TextMessage("assistant", "done")}, MaxOutputTokens: 10})
				if err != nil {
					t.Fatal(err)
				}
				var value map[string]any
				if err = json.Unmarshal(body, &value); err != nil {
					t.Fatal(err)
				}
				var parts []any
				switch p {
				case OpenAIChat:
					messages := value["messages"].([]any)
					if len(messages) != 3 {
						t.Fatalf("split user attachment into another message: %s", body)
					}
					parts = messages[1].(map[string]any)["content"].([]any)
				case OpenAIResponses:
					messages := value["input"].([]any)
					if len(messages) != 2 {
						t.Fatalf("split user attachment into another message: %s", body)
					}
					parts = messages[0].(map[string]any)["content"].([]any)
				case AnthropicMessages:
					parts = value["messages"].([]any)[0].(map[string]any)["content"].([]any)
				case Gemini:
					parts = value["contents"].([]any)[0].(map[string]any)["parts"].([]any)
				}
				expected := 2
				if text != "" {
					expected++
				}
				if len(parts) != expected {
					t.Fatalf("missing native parts: %s", body)
				}
				if !strings.Contains(string(body), pdf) || !strings.Contains(string(body), "AQID") {
					t.Fatal("model did not receive bytes", string(body))
				}
				if strings.Contains(string(body), "pot-file:") {
					t.Fatal("task link used for upload")
				}
			})
		}
	}
}

func TestUnresolvedAttachmentCannotBecomeAnEmptyModelMessage(t *testing.T) {
	msg := TextMessage("user", "")
	msg.Attachments = []core.Attachment{{ID: "pdf", Name: "guide.pdf", MIMEType: "application/pdf", Size: 13}}
	if _, err := Encode(Request{Protocol: OpenAIResponses, Model: "test", Messages: []Message{msg}, MaxOutputTokens: 10}); err == nil {
		t.Fatal("unresolved reference silently omitted")
	}
}
