package plugin

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
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
	for _, name := range []string{"github", "computer-use"} {
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
		for name := range bundle.Files {
			if strings.HasPrefix(name, "icon.") || strings.HasPrefix(name, "icon-") {
				t.Fatal("presentation asset entered tool snapshot")
			}
		}
	}
}

func TestPackagePNGIconValidation(t *testing.T) {
	p := Package{Root: t.TempDir()}
	for _, width := range []int{32, 513} {
		var data bytes.Buffer
		if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, 32))); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.Root, "icon.png"), data.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		icon, err := p.Icon()
		if width == 32 && (err != nil || !strings.HasPrefix(icon, "data:image/png;base64,")) {
			t.Fatal("valid PNG rejected", err)
		}
		if width == 513 && err == nil {
			t.Fatal("oversized PNG accepted")
		}
		if err := os.WriteFile(filepath.Join(p.Root, "icon.png"), data.Bytes()[:24], 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Icon(); err == nil {
			t.Fatal("truncated PNG accepted")
		}
	}
}
