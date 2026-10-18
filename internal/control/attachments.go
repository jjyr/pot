package control

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/jjyr/pot/internal/attachments"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

// Inspect durable projections directly: opening a Conversation here could
// acquire its lock recursively or restart pending work during garbage collection.
func (s *Service) attachmentReferenced(owner, id string) (bool, error) {
	path := filepath.Join(s.options.Root, "assistants", owner, "conversation.sqlite")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return false, err
	}
	defer db.Close()
	var found bool
	err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages,json_each(messages.body,'$.attachments') AS attachment WHERE json_extract(attachment.value,'$.id')=?)`, id).Scan(&found)
	return found, err
}
func (s *Service) UploadAttachment(ctx context.Context, owner string, u attachments.Upload) (attachments.Status, error) {
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return attachments.Status{}, err
	}
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		return attachments.Status{}, err
	}
	return s.Attachments.Upload(owner, u, snapshot.Agent.Attachments)
}
func (s *Service) resolveAttachments(owner, turn string) func(context.Context, core.ResolvedSpec, []modelprotocol.Message) ([]modelprotocol.Message, error) {
	return func(ctx context.Context, spec core.ResolvedSpec, messages []modelprotocol.Message) ([]modelprotocol.Message, error) {
		out := append([]modelprotocol.Message(nil), messages...)
		caps := spec.AttachmentCapabilities()
		var total uint64
		for i, message := range out {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(message.Attachments) == 0 {
				continue
			}
			// Re-resolution replaces only user attachment bytes. Tool images are separate.
			if message.Role != "user" {
				return nil, errors.New("user attachments require user role")
			}
			message.Images = nil
			message.Documents = nil
			for _, a := range message.Attachments {
				if !caps.Allows(a.MIMEType) {
					if message.Kind.Key == turn {
						return nil, errors.New("current model does not support this attachment type")
					}
					message.Text += fmt.Sprintf("\n[Earlier attachment %q (%s) is unavailable to the current model. Its contents are omitted.]", a.Name, a.MIMEType)
					continue
				}
				total += a.Size
				if total > 64*1024*1024 {
					return nil, errors.New("attachment context exceeds 64 MiB")
				}
				raw, err := s.Attachments.Read(owner, a)
				if err != nil {
					return nil, err
				}
				data := base64.StdEncoding.EncodeToString(raw)
				if a.MIMEType == "application/pdf" {
					message.Documents = append(message.Documents, modelprotocol.ModelDocument{Name: a.Name, Data: data})
				} else {
					message.Images = append(message.Images, modelprotocol.ModelImage{MIMEType: a.MIMEType, Data: data, Width: a.Width, Height: a.Height})
				}
			}
			message.Attachments = nil // Expanded copies are transient; the checkpoint retains refs.
			out[i] = message
		}
		return out, nil
	}
}
