package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackageIconsAreLocalAndNonExecutable(t *testing.T) {
	p := Package{Root: t.TempDir()}
	for _, svg := range []string{`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`, `<svg><image href="https://example.com"/></svg>`, `<svg onload="alert(1)"/>`, `<svg><path fill="url(https://example.com)"/></svg>`} {
		if err := os.WriteFile(filepath.Join(p.Root, "icon.svg"), []byte(svg), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Icon(); err == nil {
			t.Fatal("unsafe icon accepted", svg)
		}
	}
	for _, name := range []string{"github", "computer-use", "codex"} {
		installed, err := Load(filepath.Join("..", "..", "plugins", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = installed.Icon(); err != nil {
			t.Fatal(name, err)
		}
		bundle, err := installed.ToolBundle()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := bundle.Files["icon.svg"]; exists {
			t.Fatal("presentation asset entered tool snapshot")
		}
	}
}
