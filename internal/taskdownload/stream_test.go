package taskdownload

import (
	"bytes"
	"context"
	"encoding/base64"
	"github.com/jjyr/pot/internal/protocol"
	"strings"
	"testing"
)

func TestStreamingFencesMutationAndOffsets(t *testing.T) {
	first := protocol.FileChunk{Size: 6, Offset: 0, Revision: "one", Data: base64.StdEncoding.EncodeToString([]byte("abc"))}
	for _, kind := range []string{"ok", "revision", "size", "offset", "empty"} {
		t.Run(kind, func(t *testing.T) {
			var out bytes.Buffer
			err := Copy(context.Background(), &out, first, func(_ context.Context, offset uint64, revision string) (protocol.FileChunk, error) {
				if offset != 3 || revision != "one" {
					t.Fatal("lost download binding")
				}
				c := protocol.FileChunk{Size: 6, Offset: 3, Revision: "one", Data: base64.StdEncoding.EncodeToString([]byte("def"))}
				switch kind {
				case "revision":
					c.Revision = "two"
				case "size":
					c.Size = 7
				case "offset":
					c.Offset = 2
				case "empty":
					c.Data = ""
				}
				return c, nil
			})
			if kind == "ok" {
				if err != nil || out.String() != "abcdef" {
					t.Fatal(out.String(), err)
				}
			} else if err == nil || out.String() != "abc" {
				t.Fatal("invalid chunk published", out.String(), err)
			}
		})
	}
	first.Size = Limit + 1
	if _, err := Validate(first, 0, "", 0); err == nil {
		t.Fatal("file limit ignored")
	}
	first.Size = 1
	first.Data = strings.Repeat("A", int(ChunkSize*2))
	if _, err := Validate(first, 0, "", 0); err == nil {
		t.Fatal("unbounded chunk accepted")
	}
}
