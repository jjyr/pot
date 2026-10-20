// Frozen renderer for the legacy 0011 default-avatar migration. Product code
// uses character artwork; these pixels exist only to identify/upgrade old data.
package storage

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"sync"

	"golang.org/x/image/draw"
)

const legacyAvatarSize = 128

//go:embed legacy_avatar_mask.png
var legacyAvatarMaskPNG []byte

var legacyAvatarMask = sync.OnceValue(func() image.Image {
	value, err := png.Decode(bytes.NewReader(legacyAvatarMaskPNG))
	if err != nil {
		panic(err) // Invalid bundled artwork is a build error.
	}
	return value
})

var legacyAvatarForeground = [...]uint32{0x276451, 0x904e39, 0x685083, 0x3a6590, 0x8a6928, 0x8b4a68, 0x40546c, 0x5a6f3c}
var legacyAvatarBackground = [...]uint32{0xdce9e2, 0xf5ddd2, 0xe5ddf1, 0xdce8f4, 0xf4e8c8, 0xf1dce6, 0xdde4ec, 0xe3e8d9}

// legacyDefaultAvatar must remain byte-stable for historical migration receipts.
func legacyDefaultAvatar(seed string) (string, error) {
	hash := sha256.Sum256([]byte(seed))
	rgb := func(value uint32) color.NRGBA {
		return color.NRGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255}
	}
	dest := image.NewNRGBA(image.Rect(0, 0, legacyAvatarSize, legacyAvatarSize))
	draw.Draw(dest, dest.Bounds(), image.NewUniform(rgb(legacyAvatarBackground[hash[1]%8])), image.Point{}, draw.Src)
	alpha := image.NewNRGBA(dest.Bounds())
	// Match the original mark's 4% inset at the normalized image size.
	draw.CatmullRom.Scale(alpha, image.Rect(5, 5, legacyAvatarSize-5, legacyAvatarSize-5), legacyAvatarMask(), legacyAvatarMask().Bounds(), draw.Src, nil)
	draw.DrawMask(dest, dest.Bounds(), image.NewUniform(rgb(legacyAvatarForeground[hash[0]%8])), image.Point{}, alpha, image.Point{}, draw.Over)
	var output bytes.Buffer
	if err := png.Encode(&output, dest); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(output.Bytes()), nil
}
