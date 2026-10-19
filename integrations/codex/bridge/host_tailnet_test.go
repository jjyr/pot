package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexTailscaleOnlyCreatesUnclaimedServe(t *testing.T) {
	own := `{"TCP":{"443":{"HTTPS":true}},"Web":{"codex.tail.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8792"}}}}}`
	for _, tc := range []struct {
		name, config string
		create       bool
		error        string
	}{
		{"empty", "null", true, ""}, {"own", own, false, ""}, {"unrelated", `{"Web":{"other.tail.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}}}`, false, "in_use"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "tailscale")
			script := `#!/bin/sh
if [ "$1" = status ]; then cat "$0.status"; exit 0; fi
if [ "$2" = status ]; then cat "$0.config"; exit 0; fi
if [ "$2" = --bg ]; then cp "$0.expected" "$0.config"; echo configured >> "$0.calls"; exit 0; fi
exit 1
`
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			for suffix, data := range map[string]string{".status": `{"BackendState":"Running","Self":{"DNSName":"codex.tail.ts.net."}}`, ".config": tc.config, ".expected": own} {
				if err := os.WriteFile(binary+suffix, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			endpoint, err := configureHostTailscaleBinary(context.Background(), binary)
			if tc.error != "" {
				if err == nil || !strings.Contains(err.Error(), tc.error) {
					t.Fatal(endpoint, err)
				}
			} else if err != nil || endpoint != "https://codex.tail.ts.net" {
				t.Fatal(endpoint, err)
			}
			_, err = os.Stat(binary + ".calls")
			if tc.create && err != nil || !tc.create && !os.IsNotExist(err) {
				t.Fatal("unexpected configuration write", err)
			}
		})
	}
}
