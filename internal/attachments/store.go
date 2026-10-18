// Package attachments stores original user bytes at the owning Control. Uploads
// are bounded, resumable and idempotent; references never resolve client paths.
package attachments

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

const ChunkSize uint64 = 192 * 1024
const StoreBytes uint64 = 1024 * 1024 * 1024
const MaxRecords = 4096
const DraftLifetime = 7 * 24 * time.Hour

type Record struct {
	core.Attachment
	Owner         string `json:"owner"`
	Ready         bool   `json:"ready"`
	Touched       int64  `json:"touched"`
	ReservedUntil int64  `json:"reserved_until,omitempty"`
}
type Upload struct {
	Action   string `json:"action"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     uint64 `json:"size,omitempty"`
	Offset   uint64 `json:"offset,omitempty"`
	Data     string `json:"data,omitempty"`
}
type Status struct {
	Attachment core.Attachment `json:"attachment"`
	Offset     uint64          `json:"offset"`
	Ready      bool            `json:"ready"`
}
type Store struct {
	mu   sync.Mutex
	root string
	// Referenced checks durable messages, including steering and queued inputs.
	// Errors retain files. It must not acquire a Conversation lock.
	Referenced func(owner, id string) (bool, error)
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}
func (s *Store) path(id, ext string) string { return filepath.Join(s.root, id+ext) }
func (s *Store) record(owner, id string) (Record, error) {
	var r Record
	if !core.Identifier(owner) || !core.Identifier(id) {
		return r, errors.New("invalid attachment identity")
	}
	raw, err := os.ReadFile(s.path(id, ".json"))
	if err != nil {
		return r, errors.New("attachment unavailable")
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if r.Owner != owner || r.ID != id {
		return Record{}, errors.New("attachment does not belong to this assistant")
	}
	return r, nil
}
func (s *Store) save(r Record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, "meta-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(raw); err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err != nil {
		return err
	}
	return os.Rename(name, s.path(r.ID, ".json"))
}
func validName(name string) bool {
	return name != "" && len(name) <= 255 && !strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl) && name != "." && name != ".."
}
func (s *Store) inUse(r Record) (bool, error) {
	if s.Referenced == nil {
		return false, nil
	}
	return s.Referenced(r.Owner, r.ID)
}
func (s *Store) remove(r Record) error {
	if err := os.Remove(s.path(r.ID, ".blob")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(s.path(r.ID, ".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Sweep is also run on begin. It never removes a durable message's original.
func (s *Store) Sweep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.inventory()
	return err
}
func (s *Store) inventory() (uint64, int, error) {
	files, err := os.ReadDir(s.root)
	if err != nil {
		return 0, 0, err
	}
	var total uint64
	count := 0
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			// Account for interrupted creation before its metadata was renamed.
			// Expired orphan bytes and metadata temporaries have no references.
			info, err := f.Info()
			if err != nil {
				return 0, 0, err
			}
			orphan := strings.HasPrefix(f.Name(), "meta-")
			if strings.HasSuffix(f.Name(), ".blob") {
				_, err := os.Stat(filepath.Join(s.root, strings.TrimSuffix(f.Name(), ".blob")+".json"))
				orphan = os.IsNotExist(err)
			}
			if orphan {
				if time.Since(info.ModTime()) > DraftLifetime {
					if err := os.Remove(filepath.Join(s.root, f.Name())); err != nil {
						return 0, 0, err
					}
				} else {
					total += uint64(info.Size())
					count++
				}
			}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, f.Name()))
		if err != nil {
			return 0, 0, err
		}
		var r Record
		if err = json.Unmarshal(raw, &r); err != nil {
			return 0, 0, err
		}
		if time.Since(time.UnixMilli(r.Touched)) > DraftLifetime {
			used, err := s.inUse(r)
			if err != nil {
				return 0, 0, err
			}
			if !used {
				if err = s.remove(r); err != nil {
					return 0, 0, err
				}
				continue
			}
		}
		total += r.Size
		count++
	}
	return total, count, nil
}
func (s *Store) Upload(owner string, u Upload, caps core.AttachmentCapabilities) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out Status
	if !core.Identifier(owner) || !core.Identifier(u.ID) {
		return out, errors.New("invalid attachment identity")
	}
	r, err := s.record(owner, u.ID)
	if u.Action == "begin" {
		if !validName(u.Name) || u.Size == 0 || u.Size > core.AttachmentBytes || !caps.Allows(u.MIMEType) {
			return out, errors.New("unsupported attachment type or size (maximum 8 MiB)")
		}
		if err == nil {
			if r.Name != u.Name || r.Size != u.Size || r.MIMEType != u.MIMEType {
				return out, errors.New("upload identity already has different content")
			}
		} else {
			if _, stat := os.Stat(s.path(u.ID, ".json")); !os.IsNotExist(stat) {
				return out, err
			}
			total, count, err := s.inventory()
			if err != nil {
				return out, err
			}
			if total+u.Size > StoreBytes || count >= MaxRecords {
				return out, errors.New("attachment storage limit reached")
			}
			r = Record{Attachment: core.Attachment{ID: u.ID, Name: u.Name, MIMEType: u.MIMEType, Size: u.Size}, Owner: owner, Touched: time.Now().UnixMilli()}
			f, err := os.OpenFile(s.path(u.ID, ".blob"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return out, err
			}
			if err = f.Close(); err != nil {
				return out, err
			}
			if err = s.save(r); err != nil {
				os.Remove(s.path(u.ID, ".blob"))
				return out, err
			}
		}
	} else if err != nil {
		return out, err
	}
	if u.Action == "remove" {
		if r.ReservedUntil > time.Now().UnixMilli() {
			return out, errors.New("attachment is being accepted by a conversation")
		}
		used, err := s.inUse(r)
		if err != nil {
			return out, err
		}
		if used {
			return out, errors.New("cannot remove an attachment in conversation history")
		}
		return out, s.remove(r)
	}
	if !caps.Allows(r.MIMEType) {
		return out, errors.New("current model does not support this attachment type")
	}
	file, err := os.OpenFile(s.path(u.ID, ".blob"), os.O_RDWR, 0600)
	if err != nil {
		return out, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return out, err
	}
	offset := uint64(info.Size())
	switch u.Action {
	case "begin":
	case "chunk":
		if len(u.Data) > int((ChunkSize+2)/3*4) {
			return out, errors.New("attachment chunk too large")
		}
		data, err := base64.StdEncoding.DecodeString(u.Data)
		if err != nil || len(data) == 0 || uint64(len(data)) > ChunkSize || u.Offset > r.Size || uint64(len(data)) > r.Size-u.Offset {
			return out, errors.New("invalid attachment chunk")
		}
		if u.Offset < offset {
			old := make([]byte, len(data))
			if _, err = file.ReadAt(old, int64(u.Offset)); err != nil || !bytes.Equal(old, data) {
				return out, errors.New("retried chunk differs from stored bytes")
			}
		} else {
			if r.Ready || u.Offset != offset {
				return out, errors.New("invalid attachment offset")
			}
			if _, err = file.WriteAt(data, int64(offset)); err != nil {
				return out, err
			}
			if err = file.Sync(); err != nil {
				return out, err
			}
			offset += uint64(len(data))
		}
	case "finish":
		if offset != r.Size {
			return out, errors.New("attachment upload is incomplete")
		}
		if !r.Ready {
			data, err := io.ReadAll(io.LimitReader(file, int64(core.AttachmentBytes)+1))
			if err != nil {
				return out, err
			}
			if err = validateBytes(&r, data); err != nil {
				return out, err
			}
			r.Ready = true
		}
	default:
		return out, errors.New("invalid attachment operation")
	}
	r.Touched = time.Now().UnixMilli()
	if err = s.save(r); err != nil {
		return out, err
	}
	return Status{Attachment: r.Attachment, Offset: offset, Ready: r.Ready}, nil
}
func validateBytes(r *Record, data []byte) error {
	if uint64(len(data)) != r.Size {
		return errors.New("attachment size mismatch")
	}
	if r.MIMEType == "application/pdf" {
		if !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data[max(0, len(data)-2048):], []byte("%%EOF")) {
			return errors.New("invalid PDF file")
		}
		return nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid image")
	}
	mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp"}[format]
	if mime != r.MIMEType || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8192 || cfg.Height > 8192 || uint64(cfg.Width)*uint64(cfg.Height) > 32*1024*1024 {
		return errors.New("image type or dimensions exceed limits (8192 px, 32 megapixels)")
	}
	r.Width = uint32(cfg.Width)
	r.Height = uint32(cfg.Height)
	return nil
}
func (s *Store) Validate(owner string, items []core.Attachment, caps core.AttachmentCapabilities) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := core.ValidateAttachments(items); err != nil {
		return err
	}
	for _, a := range items {
		r, err := s.record(owner, a.ID)
		if err != nil {
			return err
		}
		if !r.Ready || r.Attachment != a {
			return errors.New("attachment is incomplete or metadata changed")
		}
		if !caps.Allows(a.MIMEType) {
			return fmt.Errorf("current model does not support %s", a.MIMEType)
		}
		// Reserve while acceptance persists its durable message; another client
		// cannot delete between validation and the conversation transaction.
		r.Touched = time.Now().UnixMilli()
		r.ReservedUntil = time.Now().Add(time.Hour).UnixMilli()
		if err = s.save(r); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Read(owner string, a core.Attachment) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.record(owner, a.ID)
	if err != nil {
		return nil, err
	}
	if !r.Ready || r.Attachment != a {
		return nil, errors.New("attachment unavailable or metadata changed")
	}
	data, err := os.ReadFile(s.path(a.ID, ".blob"))
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) != a.Size {
		return nil, errors.New("attachment size changed")
	}
	return data, nil
}
func (s *Store) Chunk(owner, id string, offset uint64) (protocol.FileChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.record(owner, id)
	if err != nil {
		return protocol.FileChunk{}, err
	}
	if !r.Ready || offset > r.Size {
		return protocol.FileChunk{}, errors.New("attachment not ready or offset invalid")
	}
	file, err := os.Open(s.path(id, ".blob"))
	if err != nil {
		return protocol.FileChunk{}, err
	}
	defer file.Close()
	data := make([]byte, min(ChunkSize, r.Size-offset))
	if n, readErr := file.ReadAt(data, int64(offset)); n != len(data) || readErr != nil && readErr != io.EOF {
		err = errors.New("attachment changed during read")
		return protocol.FileChunk{}, err
	}
	return protocol.FileChunk{Size: r.Size, Offset: offset, Revision: r.ID, Data: base64.StdEncoding.EncodeToString(data)}, nil
}
