//go:build ignore

// Generate the screenshot fixture's saved default using the product renderer.
// Run from the repository root: mise exec -- go run scripts/tests/fixtures/product-overview-avatar.go
package main

import (
	"encoding/base64"
	"os"
	"strings"

	"github.com/jjyr/pot/pkg/assistantavatar"
)

func main() {
	image, err := assistantavatar.Render(assistantavatar.ForName("Development", "build"), "build")
	if err != nil {
		panic(err)
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, "data:image/png;base64,"))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("scripts/tests/fixtures/product-overview-avatar.png", png, 0o644); err != nil {
		panic(err)
	}
}
