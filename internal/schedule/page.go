package schedule

import (
	"context"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"time"
)

func label(ms int64, spec protocol.ScheduleSpec) string {
	zone, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		return ""
	}
	return time.UnixMilli(ms).In(zone).Format("2006-01-02 15:04 MST")
}
func (s *Service) Page(ctx context.Context, owner string, query Query) (protocol.SchedulePage, error) {
	limit := query.Limit
	if limit == 0 {
		limit = 20
	}
	if limit > 50 || query.Scope != "" && query.Scope != "all" && query.Scope != "enabled" && query.Scope != "paused" {
		return protocol.SchedulePage{}, fmt.Errorf("invalid schedule query")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM schedules WHERE assistant_id=? ORDER BY id", owner)
	if err != nil {
		return protocol.SchedulePage{}, err
	}
	plans := []protocol.ScheduleInfo{}
	var selected *Plan
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return protocol.SchedulePage{}, err
		}
		var plan Plan
		if err := core.DecodeJSON([]byte(raw), &plan); err != nil {
			rows.Close()
			return protocol.SchedulePage{}, err
		}
		if plan.Info.ID == query.Selected {
			copy := plan
			selected = &copy
		}
		if plan.Info.Deleted || query.Scope == "enabled" && !plan.Info.Spec.Enabled || query.Scope == "paused" && plan.Info.Spec.Enabled {
			continue
		}
		info := plan.Info
		info.Spec.Goal = ""
		info.Spec.BeforeRun = nil
		if info.NextAtMS != nil {
			info.NextLabel = label(*info.NextAtMS, info.Spec)
		}
		plans = append(plans, info)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return protocol.SchedulePage{}, err
	}
	rows.Close()
	page := protocol.SchedulePage{Items: []protocol.ScheduleInfo{}, Runs: []protocol.ScheduleRun{}, Total: uint64(len(plans))}
	offset := query.Offset
	if offset >= page.Total && page.Total > 0 {
		offset = (page.Total - 1) / limit * limit
	}
	if page.Total == 0 {
		offset = 0
	}
	end := min(offset+limit, page.Total)
	page.Offset = offset
	page.Items = plans[offset:end]
	if end < page.Total {
		page.NextOffset = &end
	}
	if selected == nil {
		return page, nil
	}
	info := selected.Info
	if info.NextAtMS != nil {
		info.NextLabel = label(*info.NextAtMS, info.Spec)
	}
	page.Selected = &info
	rows, err = s.db.QueryContext(ctx, "SELECT body FROM schedule_runs WHERE assistant_id=? AND schedule_id=? ORDER BY scheduled DESC,rowid DESC LIMIT ? OFFSET ?", owner, info.ID, limit+1, query.RunsOffset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return page, err
		}
		var run protocol.ScheduleRun
		if err := core.DecodeJSON([]byte(raw), &run); err != nil {
			return page, err
		}
		run.ScheduledLabel = label(run.ScheduledForMS, info.Spec)
		page.Runs = append(page.Runs, run)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if uint64(len(page.Runs)) > limit {
		page.Runs = page.Runs[:limit]
		next := query.RunsOffset + limit
		page.RunsNextOffset = &next
	}
	return page, nil
}
