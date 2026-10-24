package resources

import (
	"strings"
	"testing"
)

func TestExplicitMarkdownLinks(t *testing.T) {
	source := "[中文标题](</project/有 空格/图(一).png>)\n\nSee [位置](/project/a.go:120) and ![图](artifacts/a(b).png).\n\n`[代码](/no)`\n\n```md\n[代码](/no)\n```\n\n    [缩进](/no)\n\n/home/user/bare\n\\[escaped](/no)\n[网页](https://example.com/a)\n[Task](pot-file://task/a.md)\n"
	refs := ParseLinks(source)
	if len(refs) != 5 {
		t.Fatalf("%+v", refs)
	}
	for i, want := range []string{"/project/有 空格/图(一).png", "/project/a.go:120", "artifacts/a(b).png", "https://example.com/a", "pot-file://task/a.md"} {
		if refs[i].Href != want {
			t.Fatalf("%+v", refs[i])
		}
		if source[refs[i].Start:refs[i].End] == "" {
			t.Fatal("missing span")
		}
	}
	if !refs[0].Block || refs[1].Block || !refs[2].Image {
		t.Fatal(refs)
	}
	if FileHref(refs[3].Href) || !FileHref(refs[4].Href) {
		t.Fatal("scheme handling")
	}
}
func TestHTMLCollectStaticGraph(t *testing.T) {
	files := map[string]string{"style.css": `body{background:url('pixel.png')}`, "pixel.png": "image", "local.js": "window.x=1"}
	paths, _, err := CollectHTML("index.html", []byte(`<link rel="stylesheet" href="style.css"><script src="local.js"></script><img src="pixel.png">`), func(path string, limit int) ([]byte, error) { return []byte(files[path]), nil })
	if err != nil || len(paths) != 4 {
		t.Fatal(paths, err)
	}
	for _, html := range []string{`<script>fetch("missing.json")</script>`, `<script src="https://example.com/x.js"></script>`, `<script type="module">import x from './x.js'</script>`, `<img src="../outside">`, `<img srcset="x.png 2x">`} {
		if _, _, err := CollectHTML("index.html", []byte(html), func(string, int) ([]byte, error) { t.Fatal("unsafe dependency read"); return nil, nil }); err == nil {
			t.Fatal("unsupported dependency accepted", html)
		}
	}
}

func TestMarkdownTitlesAndMultilineCode(t *testing.T) {
	for _, source := range []string{`[title](<path with (spaces).md> "optional title")`, `[title](path.md "optional title")`} {
		links := ParseLinks(source)
		if len(links) != 1 || strings.Contains(links[0].Href, "optional") {
			t.Fatal(links)
		}
	}
	if links := ParseLinks("before `code\n[not file](/no)` after [yes](file.md:3)"); len(links) != 1 || links[0].Href != "file.md:3" {
		t.Fatal(links)
	}
	if !FileHref("file.md:3") {
		t.Fatal("relative line reference rejected")
	}
	if got := DetectType("misleading.html", []byte("PK\x03\x04\x00\x00\x00\x00")); got == "text/html" {
		t.Fatal("binary suffix selected active HTML")
	}
}

func TestMarkdownContainerFencesAreNeverFileReads(t *testing.T) {
	for _, source := range []string{"> ```md\n> [not a file](/no)\n> ```\n[yes](yes.txt)", "- ```md\n  [not a file](/no)\n  ```\n\n[yes](yes.txt)", "> > ~~~\n> > ![not a file](/no)\n> > ~~~\n[yes](yes.txt)"} {
		refs := ParseLinks(source)
		if len(refs) != 1 || refs[0].Href != "yes.txt" {
			t.Fatal(refs)
		}
	}
}

func TestNumericURIsDoNotBecomeFileLineReferences(t *testing.T) {
	for _, href := range []string{"http:120", "https:120", "javascript:120", "file:120", "mailto:120", "ssh:120"} {
		if FileHref(href) {
			t.Fatal("URI became file read", href)
		}
	}
}
