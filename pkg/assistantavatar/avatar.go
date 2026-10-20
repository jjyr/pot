// Package assistantavatar composites kitchen characters over soft backgrounds.
package assistantavatar

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/draw"
)

const Size = 128

//go:embed assets/kitchen-atlas.png
var atlasPNG []byte

var names = []string{"Cup", "Mug", "Bowl", "Pan", "Wok", "Spoon", "Fork", "Tray", "Whisk", "Kettle", "Lid", "Jar", "Jug", "Plate", "Dish", "Glass"}

// Names returns the English identity catalog, independent of UI locale.
func Names() []string { return append([]string(nil), names...) }

// ForName assigns artwork once at creation. Renaming does not change the role.
func ForName(name, seed string) string {
	fields := strings.Fields(strings.ToLower(name))
	if len(fields) == 2 {
		if n, err := strconv.Atoi(fields[1]); err == nil && n >= 2 {
			fields = fields[:1]
		}
	}
	if len(fields) == 1 {
		if fields[0] == "pot" {
			return "pot"
		}
		for _, value := range names {
			if strings.EqualFold(fields[0], value) {
				return fields[0]
			}
		}
	}
	hash := sha256.Sum256([]byte(seed))
	return strings.ToLower(names[int(hash[0])%len(names)])
}

var regions = map[string]image.Rectangle{
	"pot": image.Rect(25, 45, 278, 315), "cup": image.Rect(314, 90, 555, 314),
	"mug": image.Rect(575, 92, 838, 313), "bowl": image.Rect(845, 95, 1107, 308),
	"pan": image.Rect(1108, 58, 1402, 307), "wok": image.Rect(5, 348, 316, 575),
	"spoon": image.Rect(367, 315, 525, 591), "fork": image.Rect(630, 319, 767, 592),
	"tray": image.Rect(805, 371, 1137, 574), "whisk": image.Rect(1145, 312, 1391, 594),
	"kettle": image.Rect(7, 586, 295, 841), "lid": image.Rect(296, 650, 581, 835),
	"jar": image.Rect(591, 596, 819, 849), "jug": image.Rect(858, 584, 1107, 849),
	"plate": image.Rect(1121, 594, 1396, 849), "dish": image.Rect(7, 878, 323, 1081),
	"glass": image.Rect(340, 847, 537, 1097),
}
var background = [...]uint32{0xdcece5, 0xf8dfce, 0xe7dff0, 0xd8e8f2, 0xfbefd1, 0xf2dcdf, 0xe0e7d2, 0xdfe5ea}
var sprites = sync.OnceValue(func() map[string]*image.NRGBA {
	atlas, err := png.Decode(bytes.NewReader(atlasPNG))
	if err != nil {
		panic(err)
	}
	result := make(map[string]*image.NRGBA, len(regions))
	for name, region := range regions {
		// Ignore isolated extraction dust when finding the visible artwork bounds.
		bounds := image.Rectangle{}
		for y := region.Min.Y; y < region.Max.Y; y++ {
			for x := region.Min.X; x < region.Max.X; x++ {
				_, _, _, a := atlas.At(x, y).RGBA()
				if a > 32767 {
					bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
				}
			}
		}
		if bounds.Empty() {
			panic("empty kitchen character: " + name)
		}
		sprite := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
		draw.Draw(sprite, sprite.Bounds(), atlas, bounds.Min, draw.Src)
		result[name] = sprite
	}
	return result
})

// There are only seventeen characters and eight backgrounds. Cache these bounded
// composites so resets and migration receipts do not repeatedly encode PNGs.
var rendered sync.Map

type renderKey struct {
	character  string
	background uint32
}

// Render preserves each character's colors; the creation seed selects only its
// background. Persist the result and assigned character alongside the assistant.
func Render(character, seed string) (string, error) {
	if _, ok := regions[character]; !ok {
		return "", fmt.Errorf("unknown avatar character %q", character)
	}
	hash := sha256.Sum256([]byte(seed))
	value := background[int(hash[1])%len(background)]
	key := renderKey{character, value}
	if cached, ok := rendered.Load(key); ok {
		return cached.(string), nil
	}
	sprite := sprites()[character]
	dest := image.NewNRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(dest, dest.Bounds(), image.NewUniform(color.NRGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255}), image.Point{}, draw.Src)
	width, height := Size-20, Size-20
	if sprite.Bounds().Dx() > sprite.Bounds().Dy() {
		height = width * sprite.Bounds().Dy() / sprite.Bounds().Dx()
	} else {
		width = height * sprite.Bounds().Dx() / sprite.Bounds().Dy()
	}
	rect := image.Rect((Size-width)/2, (Size-height)/2, (Size+width)/2, (Size+height)/2)
	draw.CatmullRom.Scale(dest, rect, sprite, sprite.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if err := png.Encode(&output, dest); err != nil {
		return "", err
	}
	valueURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(output.Bytes())
	rendered.Store(key, valueURL)
	return valueURL, nil
}
