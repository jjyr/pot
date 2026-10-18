package transport

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/jjyr/pot/internal/attachments"
	"github.com/jjyr/pot/pkg/core"
)

func (a *Application) attachmentCall(ctx context.Context, method string, u *url.URL, owner string, r Request) (any, error, bool) {
	if u.Path == "/api/v1/attachments" && method == "POST" {
		var upload attachments.Upload
		if err := decode(r.Body, &upload); err != nil {
			return nil, err, true
		}
		result, err := a.Control.UploadAttachment(ctx, owner, upload)
		return result, err, true
	}
	if strings.HasPrefix(u.Path, "/api/v1/attachments/") && method == "GET" {
		id := strings.TrimPrefix(u.Path, "/api/v1/attachments/")
		if !core.Identifier(id) {
			return nil, errors.New("invalid attachment identity"), true
		}
		offset := uint64(0)
		if raw := u.Query().Get("offset"); raw != "" {
			var err error
			offset, err = strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return nil, err, true
			}
		}
		// Resolve assistant ownership before reading; no local fallback for remote IDs.
		if _, err := a.Control.Conversation(ctx, owner); err != nil {
			return nil, err, true
		}
		result, err := a.Control.Attachments.Chunk(owner, id, offset)
		return result, err, true
	}
	return nil, nil, false
}
