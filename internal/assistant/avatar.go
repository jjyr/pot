package assistant

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const AvatarSourceLimit = 8 * 1024 * 1024
const AvatarSize = 128
const avatarPrefix = "data:image/png;base64,"

// NormalizeAvatar decodes bounded raster input, crops its center and stores a
// small PNG. No source paths, remote URLs, metadata or active content survive.
func NormalizeAvatar(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > AvatarSourceLimit {
		return "", errors.New("avatar source must be an image at most 8 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 32*1024*1024 {
		return "", errors.New("avatar requires PNG, JPEG or WebP within 8192 pixels and 32 megapixels")
	}
	source, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	bounds := source.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	crop := image.Rect(bounds.Min.X+(bounds.Dx()-side)/2, bounds.Min.Y+(bounds.Dy()-side)/2, bounds.Min.X+(bounds.Dx()+side)/2, bounds.Min.Y+(bounds.Dy()+side)/2)
	dest := image.NewNRGBA(image.Rect(0, 0, AvatarSize, AvatarSize))
	draw.CatmullRom.Scale(dest, dest.Bounds(), source, crop, draw.Src, nil)
	var output bytes.Buffer
	if err = png.Encode(&output, dest); err != nil {
		return "", err
	}
	return avatarPrefix + base64.StdEncoding.EncodeToString(output.Bytes()), nil
}

func ValidateAvatar(value string) error {
	if !strings.HasPrefix(value, avatarPrefix) || len(value) > 100000 {
		return errors.New("invalid normalized avatar")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, avatarPrefix))
	if err != nil {
		return errors.New("invalid normalized avatar encoding")
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width != AvatarSize || config.Height != AvatarSize {
		return errors.New("invalid normalized avatar dimensions")
	}
	if _, err = png.Decode(bytes.NewReader(raw)); err != nil {
		return errors.New("invalid normalized avatar pixels")
	}
	return nil
}
