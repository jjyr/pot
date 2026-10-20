package control

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/assistant"
)

func profileImage(t *testing.T) []byte {
	t.Helper()
	image := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 320; x++ {
			image.Set(x, y, color.NRGBA{uint8(x), uint8(y), 80, 255})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestAgentProfilePartialAtomicAndRetrySafe(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "profile", Name: "Pot"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _ := s.ExecutionWorkspace(a.ID)
	path := filepath.Join(workspace, "avatar.png")
	if err = os.WriteFile(path, profileImage(t), 0600); err != nil {
		t.Fatal(err)
	}
	call := func(id, body string) (json.RawMessage, error) {
		return s.updateAgentProfile(ctx, a.ID, id, json.RawMessage(body))
	}
	result, err := call("combined", `{"name":"牛马","avatar":{"path":"avatar.png"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "base64") || !strings.Contains(string(result), `"has_avatar":true`) {
		t.Fatal("image leaked into model result", string(result))
	}
	definitions, _ := s.Registry.Definitions(ctx)
	first := definitions[0]
	if first.Name != "牛马" || first.Revision != 2 || assistant.ValidateAvatar(first.Avatar) != nil {
		t.Fatal("profile not saved", first.Name, first.Revision)
	}
	if _, err = call("name-only", `{"name":"Later"}`); err != nil {
		t.Fatal(err)
	}
	definitions, _ = s.Registry.Definitions(ctx)
	if definitions[0].Avatar != first.Avatar {
		t.Fatal("rename removed avatar")
	}
	os.Remove(path)
	// Receipt resolution precedes opening the now-deleted source.
	if repeated, err := call("combined", `{"name":"牛马","avatar":{"path":"avatar.png"}}`); err != nil || !bytes.Equal(repeated, result) {
		t.Fatal("retry failed", err)
	}
	definitions, _ = s.Registry.Definitions(ctx)
	if definitions[0].Name != "Later" || definitions[0].Revision != 3 {
		t.Fatal("retry overwrote later profile")
	}
	if _, err = call("combined", `{"name":"Conflicting"}`); err == nil {
		t.Fatal("conflicting call accepted")
	}
	if _, err = call("invalid-atomic", `{"name":"Should not save","avatar":{"path":"missing.png"}}`); err == nil {
		t.Fatal("invalid image accepted")
	}
	definitions, _ = s.Registry.Definitions(ctx)
	if definitions[0].Name != "Later" || definitions[0].Revision != 3 {
		t.Fatal("partial update after invalid image")
	}
	for _, body := range []string{`{}`, `{"name":""}`, `{"avatar":{}}`, `{"avatar":{"path":"../outside.png"}}`, `{"avatar":{"path":"/etc/passwd"}}`, `{"avatar":{"path":"","task_id":"other"}}`, `{"avatar":{"path":"image","task_id":"unknown"}}`} {
		if _, err = call(body, body); err == nil {
			t.Fatal("invalid profile accepted", body)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	os.WriteFile(outside, profileImage(t), 0600)
	os.Symlink(outside, filepath.Join(workspace, "escape.png"))
	if _, err = call("symlink", `{"avatar":{"path":"escape.png"}}`); err == nil {
		t.Fatal("workspace symlink escape accepted")
	}
	if _, err = call("reset", `{"avatar":{"path":""}}`); err != nil {
		t.Fatal(err)
	}
	definitions, _ = s.Registry.Definitions(ctx)
	if definitions[0].Name != "Later" || definitions[0].Avatar != a.Avatar || definitions[0].Revision != 4 {
		t.Fatal("reset modified another field")
	}
}
