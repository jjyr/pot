package assistantavatar

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func decode(t *testing.T, value string) image.Image {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return img
}
func TestCharacterPixelsAndBackgrounds(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range append(Names(), "Pot") {
		character := ForName(name, "seed")
		value, err := Render(character, "seed")
		if err != nil {
			t.Fatal(err)
		}
		if seen[value] {
			t.Fatal("duplicate artwork", name)
		}
		seen[value] = true
		again, _ := Render(character, "seed")
		if again != value {
			t.Fatal("unstable image")
		}
		img := decode(t, value)
		if img.Bounds() != image.Rect(0, 0, Size, Size) {
			t.Fatal(img.Bounds())
		}
		bg := img.At(0, 0)
		pixels := 0
		for y := 0; y < Size; y++ {
			for x := 0; x < Size; x++ {
				_, _, _, a := img.At(x, y).RGBA()
				if a != 65535 {
					t.Fatal("transparent composite", name)
				}
				if img.At(x, y) != bg {
					pixels++
				}
			}
		}
		if pixels < 1000 {
			t.Fatal("missing artwork", name, pixels)
		}
	}
	first, _ := Render("mug", "seed")
	img := decode(t, first)
	backgrounds := map[color.NRGBA]bool{}
	for i := 0; i < 128; i++ {
		value, _ := Render("mug", string(rune('a'+i)))
		other := decode(t, value)
		backgrounds[color.NRGBAModel.Convert(other.At(0, 0)).(color.NRGBA)] = true
		// A fully opaque part of the body keeps its original blue across backgrounds.
		if img.At(55, 65) != other.At(55, 65) {
			t.Fatal("background seed recolored the character")
		}
	}
	if len(backgrounds) != len(background) {
		t.Fatal("background palette not covered", len(backgrounds))
	}
	if _, err := Render("missing", "seed"); err == nil {
		t.Fatal("invalid role accepted")
	}
}
func TestNameAssignment(t *testing.T) {
	for _, name := range Names() {
		if got := ForName(name+" 2", "seed"); got != strings.ToLower(name) {
			t.Fatal(got)
		}
	}
	if ForName("Pot", "seed") != "pot" {
		t.Fatal("existing Pot lost its role")
	}
	first := ForName("Renamed helper", "seed")
	if ForName("Other custom name", "seed") != first {
		t.Fatal("unknown name assignment unstable")
	}
	names := Names()
	names[0] = "changed"
	if Names()[0] != "Cup" {
		t.Fatal("mutable catalog")
	}
}
