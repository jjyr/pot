package assistantavatar

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestDefaultPaletteAndPixels(t *testing.T) {
	first, err := Default("avatar-regression-desktop-1")
	if err != nil {
		t.Fatal(err)
	}
	repeated, _ := Default("avatar-regression-desktop-1")
	second, _ := Default("avatar-regression-desktop-2")
	if repeated != first || second == first {
		t.Fatal("default image must be deterministic and seed-specific")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(first, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(raw))
	if err != nil || image.Bounds().Dx() != Size || image.Bounds().Dy() != Size {
		t.Fatal("invalid normalized image", err)
	}
	// Fixed browser regression seed: preserve its purple mark and peach background.
	bg, fg := color.NRGBA{245, 221, 210, 255}, color.NRGBA{104, 80, 131, 255}
	if color.NRGBAModel.Convert(image.At(0, 0)) != bg {
		t.Fatal("background palette changed")
	}
	markPixels := 0
	for y := 0; y < Size; y++ {
		for x := 0; x < Size; x++ {
			pixel := color.NRGBAModel.Convert(image.At(x, y)).(color.NRGBA)
			if pixel.A != 255 {
				t.Fatal("default image must be opaque")
			}
			if pixel == fg {
				markPixels++
			}
		}
	}
	if markPixels < 1000 {
		t.Fatal("Companion mark is missing", markPixels)
	}
}
