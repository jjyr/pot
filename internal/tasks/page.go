package tasks

import (
	"context"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"strings"
)

func (s *Service) Page(ctx context.Context, assistant string, query protocol.TaskQuery) (protocol.TaskPage, error) {
	limit := query.Limit
	if limit == 0 {
		limit = 10
	}
	if limit > 50 {
		return protocol.TaskPage{}, fmt.Errorf("Task page limit exceeds 50")
	}
	scope := query.Scope
	if scope == "" {
		scope = "active"
	}
	if scope != "all" && scope != "active" && scope != "attention" && scope != "archived" {
		return protocol.TaskPage{}, fmt.Errorf("invalid Task scope")
	}
	var target *core.ExecutionTarget
	if query.ExecutionTarget != "" {
		var value core.ExecutionTarget
		if err := core.DecodeJSON([]byte(query.ExecutionTarget), &value); err != nil {
			return protocol.TaskPage{}, err
		}
		target = &value
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM tasks WHERE assistant_id=? ORDER BY created DESC,id", assistant)
	if err != nil {
		return protocol.TaskPage{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return protocol.TaskPage{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return protocol.TaskPage{}, err
	}
	rows.Close()
	page := protocol.TaskPage{Items: []protocol.TaskInfo{}}
	matched := []protocol.TaskInfo{}
	for _, id := range ids {
		record, err := s.Get(ctx, assistant, id)
		if err != nil {
			return page, err
		}
		if record == nil {
			return page, fmt.Errorf("Task vanished from page")
		}
		switch record.Info.Phase.Scope() {
		case "active":
			page.Counts.Active++
		case "attention":
			page.Counts.Attention++
		case "archived":
			page.Counts.Archived++
		}
		if id == query.Selected {
			info := record.Info
			page.Selected = &info
		}
		if scope != "all" && record.Info.Phase.Scope() != scope || query.Phase != nil && record.Info.Phase != *query.Phase || query.Q != "" && !strings.Contains(strings.ToLower(record.Info.Title+" "+record.Request.Text), strings.ToLower(query.Q)) {
			continue
		}
		if target != nil && (record.Info.ExecutionTarget == nil || *record.Info.ExecutionTarget != *target) {
			continue
		}
		matched = append(matched, record.Info)
	}
	page.Total = uint64(len(matched))
	offset := query.Offset
	if offset >= page.Total && page.Total > 0 {
		offset = (page.Total - 1) / limit * limit
	}
	if page.Total == 0 {
		offset = 0
	}
	end := min(offset+limit, page.Total)
	page.Offset = offset
	page.Items = matched[offset:end]
	if end < page.Total {
		page.NextOffset = &end
	}
	return page, nil
}
