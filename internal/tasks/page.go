package tasks

import (
	"context"
	"database/sql"
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
	if query.AssistantID != "" {
		if !core.Identifier(query.AssistantID) {
			return page, fmt.Errorf("invalid assistant identity")
		}
		if assistant != "" && assistant != query.AssistantID {
			return page, fmt.Errorf("assistant filter outside owner scope")
		}
		assistant = query.AssistantID
	}
	if query.Phase != nil && query.Phase.Scope() == "" {
		return page, fmt.Errorf("invalid Task phase")
	}
	scope := query.Scope
	if scope == "" {
		scope = "active"
	}
	archived := "(CASE WHEN json_valid(body) THEN json_extract(body,'$.info.archived') ELSE 0 END)"
	phases := map[string]string{
		"complete":         archived + " IS NOT TRUE AND phase='complete'",
		"unarchived":       archived + " IS NOT TRUE",
		"failed":           archived + " IS NOT TRUE AND phase='failed'",
		"interrupted":      archived + " IS NOT TRUE AND phase='interrupted'",
		"waiting_approval": archived + " IS NOT TRUE AND phase='waiting_approval'",
		"active":           archived + " IS NOT TRUE AND phase IN ('queued','waiting','running','waiting_approval')",
		"attention":        archived + " IS NOT TRUE AND phase IN ('failed','interrupted','complete','cancelled')",
		"archived":         archived + " IS TRUE", "all": "1=1",
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
		where += " AND (json_extract(body,'$.info.execution_target')=? OR json_extract(body,'$.info.agent_link.node_id')=?)"
		args = append(args, string(raw), target.RuntimeID)
	}
	// One read transaction keeps counts, clamped offsets and rows consistent.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	ownerWhere := "(?='' OR assistant_id=?)"
	// Scope counts share owner/search/target filters, independent of the selected
	// scope and phase so other views and exception reminders remain discoverable.
	if query.Q != "" {
		where += " AND instr(pot_task_lower(id || ' ' || json_extract(body,'$.info.title') || ' ' || json_extract(body,'$.request.text')),?)>0"
		args = append(args, strings.ToLower(query.Q))
	}
	countScopes := []string{"active", "attention", "archived", "complete", "failed", "interrupted", "waiting_approval"}
	columns := make([]string, 0, len(countScopes))
	for _, name := range countScopes {
		columns = append(columns, "COALESCE(SUM("+phases[name]+"),0)")
	}
	if err := tx.QueryRowContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM tasks WHERE "+where, args...).Scan(&page.Counts.Active, &page.Counts.Attention, &page.Counts.Archived, &page.Counts.Complete, &page.Counts.Failed, &page.Counts.Interrupted, &page.Counts.WaitingApproval); err != nil {
		return page, err
	}
	where += " AND " + phases[scope]
	if query.Phase != nil {
		where += " AND phase=?"
		args = append(args, *query.Phase)
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
