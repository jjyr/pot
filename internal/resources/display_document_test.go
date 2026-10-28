package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestDisplayDocumentCompleteVerifiedBundle(t *testing.T) {
	bytes := map[string][]byte{"index.html": []byte(`<html><head><link rel="stylesheet" href="style.css"></head><body><input value="saved"><img src="picture.png"><script src="app.js"></script></body></html>`), "style.css": []byte(`body{color:red}`), "app.js": []byte(`window.loaded=true`), "picture.png": {137, 80, 78, 71, 13, 10, 26, 10}}
	resource := Resource{ID: "saved", Entry: "index.html", MIMEType: "text/html"}
	for _, path := range []string{"index.html", "style.css", "app.js", "picture.png"} {
		raw := bytes[path]
		sum := sha256.Sum256(raw)
		resource.Files = append(resource.Files, File{Path: path, MIMEType: DetectType(path, raw), Size: uint64(len(raw)), Digest: hex.EncodeToString(sum[:])})
		resource.Size += uint64(len(raw))
	}
	read := func(path string) ([]byte, error) { return bytes[path], nil }
	document, err := RenderDisplayDocument(resource, read)
	if err != nil {
		t.Fatal(err)
	}
	if document.Token != "pot-resource-mount-token-v1" || document.Resource != resource.ID || document.Entry != resource.Entry || len(document.Files) != 4 {
		t.Fatalf("wrong template identity: %+v", document)
	}
	if !strings.Contains(document.HTML, "data:text/javascript;base64,") || !strings.Contains(document.HTML, "data:text/css;base64,") || !strings.Contains(document.HTML, "data:image/png;base64,") {
		t.Fatal("missing embedded dependencies")
	}
	head := strings.Index(document.HTML, "<head>")
	policy := strings.Index(document.HTML, "Content-Security-Policy")
	script := strings.Index(document.HTML, "<script>")
	if head < 0 || policy < head || policy > script {
		t.Fatal("CSP must precede executable content")
	}
	sum := sha256.Sum256([]byte(document.HTML))
	if hex.EncodeToString(sum[:]) != document.Digest {
		t.Fatal("document digest mismatch")
	}
	bytes["app.js"] = []byte(`changed`)
	if _, err := RenderDisplayDocument(resource, read); err == nil {
		t.Fatal("changed immutable bytes accepted")
	}
}
func TestDisplayDocumentRejectsIncompleteOrExternalDependencies(t *testing.T) {
	for _, source := range []string{`<img src="missing.png">`, `<script src="https://example.invalid/app.js"></script>`, `<script>fetch('extra.json')</script>`, `<style>@import 'other.css';</style>`} {
		raw := []byte(source)
		sum := sha256.Sum256(raw)
		resource := Resource{ID: "incomplete", Entry: "index.html", MIMEType: "text/html", Size: uint64(len(raw)), Files: []File{{Path: "index.html", MIMEType: "text/html", Size: uint64(len(raw)), Digest: hex.EncodeToString(sum[:])}}}
		reads := 0
		_, err := RenderDisplayDocument(resource, func(path string) ([]byte, error) {
			reads++
			if path != "index.html" {
				t.Fatalf("read outside manifest: %s", path)
			}
			return raw, nil
		})
		if err == nil || reads != 1 {
			t.Fatalf("incomplete bundle accepted: %s (%v, %d)", source, err, reads)
		}
	}
}
