package tasks

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"strings"

	"modernc.org/sqlite"
)

func init() {
	// SQLite's built-in lower only folds ASCII. Keep the existing Unicode search
	// behavior while filtering in SQL, without decoding every full Task record.
	sqlite.MustRegisterDeterministicScalarFunction("pot_task_lower", 1, func(_ *sqlite.FunctionContext, values []driver.Value) (driver.Value, error) {
		if len(values) == 1 {
			if value, ok := values[0].(string); ok {
				return strings.ToLower(value), nil
			}
		}
		return nil, fmt.Errorf("Task search requires text")
	})
}

func (s *Service) Page(ctx context.Context, assistant string, query protocol.TaskQuery) (protocol.TaskPage, error) {
	if !core.Identifier(assistant) {
		return protocol.TaskPage{}, fmt.Errorf("invalid assistant identity")
	}
	return s.page(ctx, assistant, query)
}

// Catalog is the authenticated operator's Control-wide list. Agent tools keep
// using Page with their explicit assistant; detail operations remain owner-scoped.
func (s *Service) Catalog(ctx context.Context, query protocol.TaskQuery) (protocol.TaskPage, error) {
	return s.page(ctx, "", query)
}

func (s *Service) page(ctx context.Context, assistant string, query protocol.TaskQuery) (protocol.TaskPage, error) {
	page := protocol.TaskPage{Items: []protocol.TaskInfo{}}
	limit := query.Limit
	if limit == 0 {
		limit = 10
	}
	if limit > 50 {
		return page, fmt.Errorf("Task page limit exceeds 50")
	}
	scope := query.Scope
	if scope == "" {
		scope = "active"
	}
	phases := map[string]string{
		"active":    "phase IN ('queued','waiting','running','waiting_approval')",
		"attention": "phase IN ('failed','interrupted')",
		"archived":  "phase IN ('complete','cancelled')", "all": "1=1",
	}
	if phases[scope] == "" {
		return page, fmt.Errorf("invalid Task scope")
	}
	where, args := "(?='' OR assistant_id=?)", []any{assistant, assistant}
	if query.ExecutionTarget != "" {
		var target core.ExecutionTarget
		if err := core.DecodeJSON([]byte(query.ExecutionTarget), &target); err != nil {
			return page, err
		}
		raw, err := json.Marshal(target)
		if err != nil {
			return page, err
		}
		where += " AND json_extract(body,'$.info.execution_target')=?"
		args = append(args, string(raw))
	}
	// One read transaction keeps counts, clamped offsets and rows consistent.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	ownerWhere := "(?='' OR assistant_id=?)"
	if err := tx.QueryRowContext(ctx, `SELECT
	 COALESCE(SUM(phase IN ('queued','waiting','running','waiting_approval')),0),
	 COALESCE(SUM(phase IN ('failed','interrupted')),0),
	 COALESCE(SUM(phase IN ('complete','cancelled')),0) FROM tasks WHERE `+ownerWhere, assistant, assistant).Scan(&page.Counts.Active, &page.Counts.Attention, &page.Counts.Archived); err != nil {
		return page, err
	}
	where += " AND " + phases[scope]
	if query.Phase != nil {
		where += " AND phase=?"
		args = append(args, *query.Phase)
	}
	if query.Q != "" {
		where += " AND instr(pot_task_lower(id || ' ' || json_extract(body,'$.info.title') || ' ' || json_extract(body,'$.request.text')),?)>0"
		args = append(args, strings.ToLower(query.Q))
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE "+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	offset := query.Offset
	if offset >= page.Total && page.Total > 0 {
		offset = (page.Total - 1) / limit * limit
	}
	if page.Total == 0 {
		offset = 0
	}
	page.Offset = offset
	rows, err := tx.QueryContext(ctx, "SELECT json_extract(body,'$.info') FROM tasks WHERE "+where+" ORDER BY CASE WHEN json_valid(body) THEN json_extract(body,'$.info.updated_at_ms') END DESC,id,assistant_id LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return page, err
		}
		var info protocol.TaskInfo
		if err := core.DecodeJSON([]byte(raw), &info); err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, info)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if end := offset + uint64(len(page.Items)); end < page.Total {
		page.NextOffset = &end
	}
	if query.Selected != "" {
		var raw string
		rows, err := tx.QueryContext(ctx, "SELECT json_extract(body,'$.info') FROM tasks WHERE "+ownerWhere+" AND id=? LIMIT 1", assistant, assistant, query.Selected)
		if err != nil {
			return page, err
		}
		if rows.Next() {
			if err = rows.Scan(&raw); err == nil {
				var info protocol.TaskInfo
				err = core.DecodeJSON([]byte(raw), &info)
				page.Selected = &info
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return page, err
		}
	}
	return page, tx.Commit()
}
