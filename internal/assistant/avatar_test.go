package assistant

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
)

func TestAvatarNormalizationAndProfilePersistence(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 300, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 300; x++ {
			pixel := color.NRGBA{255, 0, 0, 255}
			if x >= 100 && x < 200 {
				pixel = color.NRGBA{0, 255, 0, 255}
			}
			source.Set(x, y, pixel)
		}
	}
	var input bytes.Buffer
	png.Encode(&input, source)
	avatar, err := NormalizeAvatar(input.Bytes())
	if err != nil || ValidateAvatar(avatar) != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(avatar, avatarPrefix))
	crop, err := png.Decode(bytes.NewReader(raw))
	if err != nil || crop.Bounds().Dx() != 128 || color.NRGBAModel.Convert(crop.At(0, 0)).(color.NRGBA).G != 255 {
		t.Fatal("center crop failed", err)
	}
	for _, bad := range [][]byte{nil, []byte(`<svg onload="alert(1)"></svg>`), bytes.Repeat([]byte{0}, AvatarSourceLimit+1), input.Bytes()[:40]} {
		if _, err = NormalizeAvatar(bad); err == nil {
			t.Fatal("bad image accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "registry.sqlite")
	r, err := OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d, err := r.Create(ctx, "create", "Pot")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := r.UpdateProfileOnce(ctx, d.ID, ProfileUpdate{Avatar: &avatar}, "avatar", "input-hash")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	r, err = OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	definitions, err := r.Definitions(ctx)
	if err != nil || definitions[0] != changed {
		t.Fatal("profile not durable", err)
	}
	renamed, err := r.Rename(ctx, d.ID, "Renamed", changed.Revision)
	if err != nil || renamed.Avatar != avatar {
		t.Fatal("settings rename lost avatar", err)
	}
	replay, err := r.Create(ctx, "create", "Pot")
	if err != nil || replay.Avatar != avatar {
		t.Fatal("creation retry lost avatar", err)
	}
	receipt, err := r.ProfileReceipt(ctx, "avatar", "input-hash")
	if err != nil || *receipt != changed {
		t.Fatal("receipt lost", err)
	}
}
