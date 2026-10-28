package agentlink

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"io"
	"strings"
	"unicode/utf8"
)

func decodeJSON(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(value); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func validIdentity(i Identity) bool {
	return i.Address.Valid() && (i.Address.Kind == "assistant" || i.Address.Kind == "node" || i.Address.Kind == "task") && core.Identifier(i.Adapter) && strings.TrimSpace(i.Name) != "" && utf8.ValidString(i.Name) && len(i.Name) <= 512
}
func (h *History) List(ctx context.Context) ([]HistoricalConnection, error) {
	rows, e := h.DB.QueryContext(ctx, "SELECT metadata FROM agent_link_history ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []HistoricalConnection{}
	for rows.Next() {
		var raw []byte
		var c HistoricalConnection
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &c); e != nil {
			return nil, e
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (h *History) Get(ctx context.Context, id string) (HistoricalConnection, error) {
	var c HistoricalConnection
	var raw []byte
	e := h.DB.QueryRowContext(ctx, "SELECT metadata FROM agent_link_history WHERE id=?", id).Scan(&raw)
	if e == sql.ErrNoRows {
		return c, ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(raw, &c)
	}
	return c, e
}
