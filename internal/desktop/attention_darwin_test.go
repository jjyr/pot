//go:build darwin

package desktop

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/draw"
	"image/png"
	"testing"
)

func TestTrayAtlasesContainWideIdleWorkingAndUnreadFrames(t *testing.T) {
	if bytes.Equal(trayLight, trayDark) {
		t.Fatal("appearance-specific artwork is missing")
	}
	for _, data := range [][]byte{trayLight, trayDark} {
		atlas, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if atlas.Bounds() != image.Rect(0, 0, 1100, 1584) {
			t.Fatal("tray atlas geometry changed", atlas.Bounds())
		}
		tile := func(x, y int) [32]byte {
			result := image.NewNRGBA(image.Rect(0, 0, 100, 44))
			draw.Draw(result, result.Bounds(), atlas, image.Pt(x*100, y*44), draw.Src)
			return sha256.Sum256(result.Pix)
		}
		idle := tile(0, 0)
		if idle == tile(1, 0) || idle == tile(0, 5) {
			t.Fatal("unread badge or working steam artwork missing")
		}
		blank := sha256.Sum256(make([]byte, 100*44*4))
		for row := range 36 {
			for column := range 11 {
				if tile(column, row) == blank {
					t.Fatalf("empty tray attention frame %d/%d", column, row)
				}
			}
		}
	}
}
