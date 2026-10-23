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

	"github.com/jjyr/pot/pkg/assistantavatar"
)

func TestSettingsProfileEditSharesRevisionAndCanRestoreDefault(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "control.sqlite"), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	d, err := r.Create(ctx, "create", "Cup 2")
	if err != nil {
		t.Fatal(err)
	}
	custom, _ := assistantavatar.Render("glass", "custom-image")
	name := "My helper"
	changed, err := r.UpdateProfile(ctx, d.ID, ProfileUpdate{Name: &name, Avatar: &custom}, d.Revision)
	if err != nil || changed.Name != name || changed.Avatar != custom || changed.Revision == d.Revision {
		t.Fatal("settings edit was not atomic", changed, err)
	}
	if _, err = r.UpdateProfile(ctx, d.ID, ProfileUpdate{Avatar: &d.Avatar}, d.Revision); err == nil {
		t.Fatal("stale settings overwrote newer profile")
	}
	bad := "data:image/png;base64,invalid"
	if _, err = r.UpdateProfile(ctx, d.ID, ProfileUpdate{Name: &d.Name, Avatar: &bad}, changed.Revision); err == nil {
		t.Fatal("invalid image accepted")
	}
	empty := ""
	reset, err := r.UpdateProfile(ctx, d.ID, ProfileUpdate{Avatar: &empty}, changed.Revision)
	if err != nil || reset.Name != name || reset.Avatar != d.Avatar {
		t.Fatal("reset lost name or original colors", reset, err)
	}
}

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
	d, err := r.Create(ctx, "create", "Cup 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAvatar(d.Avatar); err != nil {
		t.Fatal("creation did not persist a normalized default image", err)
	}
	changed, err := r.UpdateProfile(ctx, d.ID, ProfileUpdate{Avatar: &avatar}, d.Revision)
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
	replay, err := r.Create(ctx, "create", "Cup 2")
	if err != nil || replay.Avatar != avatar {
		t.Fatal("creation retry lost avatar", err)
	}
	empty := ""
	reset, err := r.UpdateProfile(ctx, d.ID, ProfileUpdate{Avatar: &empty}, renamed.Revision)
	if err != nil || reset.Avatar != d.Avatar || reset.Name != "Renamed" {
		t.Fatal("reset did not restore the persisted creation image", err)
	}
	r.Close()
	r, err = OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	definitions, err = r.Definitions(ctx)
	if err != nil || definitions[0] != reset {
		t.Fatal("default image did not survive restart", err)
	}
}
