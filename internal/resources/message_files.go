package resources

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/jjyr/pot/pkg/core"
)

// Source is minted only by the execution host, never from Markdown or HTTP.
// Root is the captured execution directory and relative-path anchor.
type Source struct {
	RootID   string `json:"root_id"`
	ConfigID string `json:"config_id,omitempty"`
	Node     string `json:"node"`
	Owner    string `json:"owner"`
	Task     string `json:"task,omitempty"`
	Run      string `json:"run,omitempty"`
	Root     string `json:"root"`
	Config   string `json:"config,omitempty"`
}
type MessageFile struct {
	ID       string `json:"id"`
	Message  string `json:"message"`
	Revision string `json:"revision"`
	Link
	Source Source `json:"-"`
	// ReceiptSource preserves the saved serialization solely for immutable receipt lookup.
	// Unknown fields never select file-reading authority.
	ReceiptSource json.RawMessage `json:"-"`
	State         string          `json:"state"`
	Resource      *Resource       `json:"resource,omitempty"`
}

func (s *Store) BindMessage(ctx context.Context, owner, conversation, message, text string, source Source) error {
	return s.BindMessageSources(ctx, owner, conversation, message, text, source, nil)
}
func (s *Store) BindMessageSources(ctx context.Context, owner, conversation, message, text string, source Source, resolve func(Link) Source) error {
	if source.Owner != owner || !core.Identifier(owner) || !core.Identifier(message) {
		return errors.New("invalid message file source binding")
	}
	revision, _ := core.HashJSON(text)
	for _, link := range ParseLinks(text) {
		if !FileHref(link.Href) {
			continue
		}
		bound := source
		if resolve != nil {
			bound = resolve(link)
		}
		if bound.Owner != owner {
			return errors.New("invalid message file owner")
		}
		raw, err := json.Marshal(bound)
		if err != nil {
			return err
		}
		id, _ := core.HashJSON([]any{owner, conversation, message, revision, link.Start, link.End, link.Href})
		_, err = s.db.ExecContext(ctx, `INSERT INTO message_files(owner,conversation,message,revision,ref,start,end,href,label,block,source,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,'pending') ON CONFLICT DO NOTHING`, owner, conversation, message, revision, id, link.Start, link.End, link.Href, link.Label, link.Block, string(raw))
		if err != nil {
			return err
		}
	}
	return nil
}

var hrefLine = regexp.MustCompile(`:[1-9][0-9]*$`)
var reservedHrefScheme = regexp.MustCompile(`(?i)^(?:https?|javascript|data|file|mailto|ftp|ssh|tel):`)
var hrefScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func FileHref(href string) bool {
	if strings.HasPrefix(href, "pot-file://") {
		return true
	}
	if reservedHrefScheme.MatchString(href) {
		return false
	}
	href = hrefLine.ReplaceAllString(href, "")
	return href != "" && !strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "//") && !hrefScheme.MatchString(href)
}
func (s *Store) MessageFiles(ctx context.Context, owner, conversation, message string) ([]MessageFile, error) {
	if s.ValidateOwner != nil {
		if err := s.ValidateOwner(ctx, owner); err != nil {
			return nil, err
		}
	}
	return s.queryFiles(ctx, `SELECT ref,message,revision,start,end,href,label,block,source,state,resource FROM message_files WHERE owner=? AND conversation=? AND message=? AND revision=(SELECT revision FROM message_files WHERE owner=? AND conversation=? AND message=? ORDER BY rowid DESC LIMIT 1) ORDER BY start`, owner, conversation, message, owner, conversation, message)
}
func (s *Store) queryFiles(ctx context.Context, query string, args ...any) ([]MessageFile, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MessageFile{}
	for rows.Next() {
		var f MessageFile
		var source, resource string
		if err = rows.Scan(&f.ID, &f.Message, &f.Revision, &f.Start, &f.End, &f.Href, &f.Label, &f.Block, &source, &f.State, &resource); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(source), &f.Source); err != nil {
			return nil, err
		}
		f.ReceiptSource = json.RawMessage(source)
		if resource != "" {
			if err = json.Unmarshal([]byte(resource), &f.Resource); err != nil {
				return nil, err
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Store) PendingFiles(ctx context.Context) ([]MessageFile, error) {
	return s.queryFiles(ctx, `SELECT ref,message,revision,start,end,href,label,block,source,state,resource FROM message_files WHERE state IN ('pending','resolving') ORDER BY rowid LIMIT 32`)
}
func (s *Store) FileState(ctx context.Context, id, state string, resource *Resource) error {
	raw := ""
	if resource != nil {
		b, err := json.Marshal(resource)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE message_files SET state=?,resource=? WHERE ref=?`, state, raw, id)
	return err
}
func (s *Store) RetryFile(ctx context.Context, owner, conversation, message, ref string) error {
	rows, err := s.MessageFiles(ctx, owner, conversation, message)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID == ref {
			if row.State == "ready" {
				return nil
			}
			return s.FileState(ctx, ref, "pending", nil)
		}
	}
	return errors.New("file reference unavailable")
}
