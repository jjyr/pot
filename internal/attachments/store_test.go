package attachments

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 2, 3))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func finishUpload(t *testing.T, s *Store, owner, id, name, mime string, data []byte) core.Attachment {
	t.Helper()
	caps := core.AttachmentCapabilities{Image: true, PDF: true}
	for _, u := range []Upload{{Action: "begin", ID: id, Name: name, MIMEType: mime, Size: uint64(len(data))}, {Action: "chunk", ID: id, Data: base64.StdEncoding.EncodeToString(data)}, {Action: "finish", ID: id}} {
		status, err := s.Upload(owner, u, caps)
		if err != nil {
			t.Fatal(err)
		}
		if u.Action == "finish" {
			return status.Attachment
		}
	}
	panic("unreachable")
}
func TestUploadValidationOwnershipAndReplay(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	caps := core.AttachmentCapabilities{Image: true, PDF: true}
	data := pngBytes(t)
	a := finishUpload(t, s, "alice", "picture", "screenshot.png", "image/png", data)
	if a.Width != 2 || a.Height != 3 {
		t.Fatal(a)
	}
	retry := Upload{Action: "chunk", ID: a.ID, Data: base64.StdEncoding.EncodeToString(data)}
	if _, err = s.Upload("alice", retry, caps); err != nil {
		t.Fatal("identical retry", err)
	}
	retry.Data = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0}, len(data)))
	if _, err = s.Upload("alice", retry, caps); err == nil {
		t.Fatal("changed chunk accepted")
	}
	if _, err = s.Chunk("bob", a.ID, 0); err == nil {
		t.Fatal("cross-owner read")
	}
	if _, err = s.Upload("bob", Upload{Action: "begin", ID: a.ID, Name: a.Name, MIMEType: a.MIMEType, Size: a.Size}, caps); err == nil {
		t.Fatal("cross-owner upload overwrite")
	}
	forged := a
	forged.Name = "other.png"
	if err = s.Validate("alice", []core.Attachment{forged}, caps); err == nil {
		t.Fatal("forged metadata accepted")
	}
	if err = s.Validate("alice", []core.Attachment{a}, core.AttachmentCapabilities{PDF: true}); err == nil {
		t.Fatal("text model accepted image")
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := reopened.Read("alice", a)
	if err != nil || !bytes.Equal(raw, data) {
		t.Fatal("original bytes after restart", err)
	}
	for _, u := range []Upload{
		{Action: "begin", ID: "path", Name: "../../outside.png", MIMEType: "image/png", Size: 10},
		{Action: "begin", ID: "oversize", Name: "x.png", MIMEType: "image/png", Size: core.AttachmentBytes + 1},
		{Action: "begin", ID: "mime", Name: "x.svg", MIMEType: "image/svg+xml", Size: 10},
		{Action: "begin", ID: "../escape", Name: "x.png", MIMEType: "image/png", Size: 10},
	} {
		if _, err = s.Upload("alice", u, caps); err == nil {
			t.Fatalf("accepted invalid upload %+v", u)
		}
	}
	if _, err = s.Upload("alice", Upload{Action: "begin", ID: "disabled", Name: "x.png", MIMEType: "image/png", Size: 10}, core.AttachmentCapabilities{PDF: true}); err == nil {
		t.Fatal("upload capability bypass")
	}
	if _, err = s.Upload("alice", Upload{Action: "begin", ID: "spoof", Name: "x.png", MIMEType: "image/png", Size: 5}, caps); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upload("alice", Upload{Action: "chunk", ID: "spoof", Data: base64.StdEncoding.EncodeToString([]byte("hello"))}, caps); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upload("alice", Upload{Action: "finish", ID: "spoof"}, caps); err == nil {
		t.Fatal("MIME spoof accepted")
	}
	pdf := finishUpload(t, s, "alice", "document", "文档.pdf", "application/pdf", []byte("%PDF-1.7\n%%EOF"))
	if pdf.MIMEType != "application/pdf" {
		t.Fatal(pdf)
	}
	if _, err = s.Chunk("alice", a.ID, a.Size+1); err == nil {
		t.Fatal("invalid download offset")
	}
}
func TestDraftCleanupKeepsHistoryAndQuota(t *testing.T) {
	s, _ := Open(t.TempDir())
	data := pngBytes(t)
	sent := finishUpload(t, s, "alice", "sent", "sent.png", "image/png", data)
	draft := finishUpload(t, s, "alice", "draft", "draft.png", "image/png", data)
	s.Referenced = func(owner, id string) (bool, error) { return owner == "alice" && id == sent.ID, nil }
	for _, a := range []core.Attachment{sent, draft} {
		r, _ := s.record("alice", a.ID)
		r.Touched = time.Now().Add(-DraftLifetime - time.Hour).UnixMilli()
		if err := s.save(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("alice", sent); err != nil {
		t.Fatal("deleted history original", err)
	}
	if _, err := os.Stat(filepath.Join(s.root, draft.ID+".blob")); !os.IsNotExist(err) {
		t.Fatal("expired draft not removed", err)
	}
	if _, err := s.Upload("alice", Upload{Action: "remove", ID: sent.ID}, core.AttachmentCapabilities{}); err == nil {
		t.Fatal("history original removable")
	}
	r, _ := s.record("alice", sent.ID)
	r.Size = StoreBytes
	if err := s.save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload("alice", Upload{Action: "begin", ID: "over-quota", Name: "x.png", MIMEType: "image/png", Size: 10}, core.AttachmentCapabilities{Image: true}); err == nil {
		t.Fatal("quota bypass")
	}
}
