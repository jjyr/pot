package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/pkg/protocol"
)

var markdownResourceSources = []string{"review.md", "review.MARKDOWN", "plain.txt", "raw.md"}

// Synthetic review content only. CRLF and Unicode also exercise original-byte
// preservation independently of the renderer's line-ending normalization.
const markdownResourceSource = "# Resource preview · 季度回顾\r\n\r\nReadable **Markdown**, with `inline code` and [documentation](https://example.com/guide).\r\n\r\n## Delivery status\r\n\r\n| Feature | Status | Notes |\r\n| --- | --- | --- |\r\n| Markdown reader | Ready | 默认阅读 · Unicode ✓ |\r\n| Original download | Preserved | No changes to file bytes |\r\n\r\n> Preview and source use the same immutable resource.\r\n\r\n- Headings, lists and tables\r\n- Safe links and readable code\r\n\r\n```js\r\nconst report = { format: 'Markdown', source: 'original bytes', unicode: '你好' };\r\nconsole.log(report);\r\n```\r\n\r\n![Remote image](https://resource-isolation.invalid/image)\r\n![Missing asset](missing.png)\r\n[Relative document](../private.txt)\r\n[Unsafe](javascript:alert%281%29)\r\n<script>window.markdownExecuted=true</script>\r\n<img src=\"https://resource-isolation.invalid/raw\" onerror=\"window.markdownExecuted=true\">\r\n"

func TestMarkdownMessageFilesAndHTTPOriginals(t *testing.T) {
	app, items := resourceApplicationSources(t, markdownResourceSources, "Renamed.html")
	owner := localAssistantID(t, app.Control)
	handler := Handler(app, "operator")
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", path+"?assistant_id="+owner, nil)
		request.Header.Set("Authorization", "Bearer operator")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, item := range items {
		prefix := "/api/v1/resources/" + item.ID
		response := get(prefix)
		var metadata resources.Resource
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &metadata) != nil || metadata.Entry != item.Entry || metadata.Name != item.Entry {
			t.Fatal("registered source identity lost", response.Code, response.Body.String())
		}
		if metadata.Entry != "raw.md" && metadata.MIMEType != "text/plain" {
			t.Fatal("fixture must exercise existing text/plain Markdown", metadata)
		}
		content := get(prefix + "/content")
		download := get(prefix + "/download")
		if content.Code != 200 || download.Code != 200 || !bytes.Equal(content.Body.Bytes(), download.Body.Bytes()) {
			t.Fatal("original download bytes differ")
		}
		if item.Entry != "raw.md" && !bytes.Equal(download.Body.Bytes(), []byte(markdownResourceSource)) {
			t.Fatal("Unicode or CRLF original changed")
		}
		response = get(prefix + "/chunk")
		var chunk protocol.FileChunk
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &chunk) != nil {
			t.Fatal("chunk unavailable")
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || !bytes.Equal(data, content.Body.Bytes()) || chunk.Revision != item.Files[0].Digest {
			t.Fatal("preview chunk changed original")
		}
		stored, err := app.Control.Resources.Get(context.Background(), owner, item.ID)
		if err != nil || stored.Entry != metadata.Entry || stored.MIMEType != metadata.MIMEType {
			t.Fatal("reading changed persisted resource metadata")
		}
	}
}
