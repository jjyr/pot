package usage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/jjyr/pot/pkg/storage"
)

// Page is a bounded list. Each resource has its own offset and query.
type Page struct {
	Items      any  `json:"items"`
	Offset     int  `json:"offset"`
	NextOffset *int `json:"next_offset"`
}

func page[T any](offset int, items []T) Page {
	p := Page{Items: items, Offset: offset}
	if len(items) > 20 {
		next := offset + 20
		p.NextOffset = &next
		p.Items = items[:20]
	}
	return p
}

func (l *Ledger) Page(ctx context.Context, kind string, q Query, atMS int64) (Page, error) {
	if q.Offset < 0 || q.Offset > 1000000 || len(q.Scope) > 128 || q.Start != "" || q.End != "" {
		return Page{}, fmt.Errorf("invalid usage query")
	}
	if q.Timezone == "" {
		q.Timezone = "Asia/Shanghai"
	}
	zone, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return Page{}, err
	}
	var value Page
	err = storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		switch kind {
		case "models":
			value, err = modelPage(ctx, tx, q, atMS, zone)
		case "requests", "tools", "compactions":
			if q.Scope == "" {
				return fmt.Errorf("usage details require a scope")
			}
			switch kind {
			case "requests":
				value, err = requestPage(ctx, tx, q, zone)
			case "tools":
				value, err = toolPage(ctx, tx, q)
			case "compactions":
				value, err = compactionPage(ctx, tx, q, zone)
			}
		default:
			return fmt.Errorf("invalid usage resource")
		}
		return err
	})
	return value, err
}
func modelPage(ctx context.Context, db queryDB, q Query, atMS int64, zone *time.Location) (Page, error) {
	if q.Days == 0 {
		q.Days = 365
	}
	if q.Days != 14 && q.Days != 30 && q.Days != 90 && q.Days != 365 {
		return Page{}, fmt.Errorf("invalid usage range")
	}
	groupStart, groupEnd := int64(0), int64(math.MaxInt64)
	if q.Scope == "" {
		at := time.UnixMilli(atMS).In(zone)
		y, m, d := at.Date()
		first := time.Date(y, m, d, 12, 0, 0, 0, time.UTC).AddDate(0, 0, 1-q.Days)
		begin, err := midnight(zone, first)
		if err != nil {
			return Page{}, err
		}
		groupStart, groupEnd = begin.UnixMilli(), atMS+1
	}
	items := []AgentModel{}

	rows, err := db.QueryContext(ctx, `SELECT COALESCE(t.agent,'unknown'),r.model,COUNT(*),COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0) FROM requests r LEFT JOIN request_routes t ON t.request_id=r.id WHERE (?='' OR r.scope=?) AND r.at>=? AND r.at<? GROUP BY COALESCE(t.agent,'unknown'),r.model ORDER BY COALESCE(SUM(r.input),0)+COALESCE(SUM(r.output),0) DESC,1,2 LIMIT 21 OFFSET ?`, q.Scope, q.Scope, groupStart, groupEnd, q.Offset)
	if err != nil {
		return Page{}, err
	}
	for rows.Next() {
		var a AgentModel
		if err := rows.Scan(&a.Agent, &a.Model, &a.Requests, &a.Input, &a.Output, &a.Cache); err != nil {
			rows.Close()
			return Page{}, err
		}
		items = append(items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Page{}, err
	}
	return page(q.Offset, items), nil
}
func requestPage(ctx context.Context, db queryDB, q Query, zone *time.Location) (Page, error) {
	items := []RequestRecord{}

	rows, err := db.QueryContext(ctx, `SELECT r.id,r.turn_id,r.at,r.model,r.input,r.output,r.cache,r.state,r.compaction_id,COALESCE(t.agent,'unknown'),COALESCE(t.provider,'unknown'),COALESCE(t.profile_id,''),t.generation FROM requests r LEFT JOIN request_routes t ON t.request_id=r.id WHERE r.scope=? ORDER BY r.at DESC,r.id DESC LIMIT 21 OFFSET ?`, q.Scope, q.Offset)
	if err != nil {
		return Page{}, err
	}
	for rows.Next() {
		var r RequestRecord
		var input, output, generation sql.NullInt64
		var compaction sql.NullString
		if err := rows.Scan(&r.ID, &r.Turn, &r.At, &r.Model, &input, &output, &r.Cache, &r.State, &compaction, &r.Agent, &r.Provider, &r.ProfileID, &generation); err != nil {
			rows.Close()
			return Page{}, err
		}
		r.Input = nullable(input)
		r.Output = nullable(output)
		r.Generation = nullable(generation)
		r.Purpose = "conversation"
		if compaction.Valid {
			r.Purpose = "compaction"
		}
		r.Label = time.UnixMilli(r.At).In(zone).Format("2006-01-02 15:04")
		if r.At == 0 {
			r.Label = "历史 · 时间未知"
		}
		items = append(items, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Page{}, err
	}
	return page(q.Offset, items), nil
}
func toolPage(ctx context.Context, db queryDB, q Query) (Page, error) {
	items := []ToolTotal{}

	rows, err := db.QueryContext(ctx, `SELECT name,COUNT(*),SUM(failed),COALESCE(SUM(bytes),0),COALESCE(SUM(MAX(end_at-at,0)),0),SUM(CASE WHEN end_at IS NULL THEN 1 ELSE 0 END) FROM tools WHERE scope=? GROUP BY name ORDER BY COUNT(*) DESC,name LIMIT 21 OFFSET ?`, q.Scope, q.Offset)
	if err != nil {
		return Page{}, err
	}
	for rows.Next() {
		var t ToolTotal
		if err := rows.Scan(&t.Name, &t.Calls, &t.Failed, &t.Bytes, &t.DurationMS, &t.Unfinished); err != nil {
			rows.Close()
			return Page{}, err
		}
		items = append(items, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Page{}, err
	}
	return page(q.Offset, items), nil
}
func compactionPage(ctx context.Context, db queryDB, q Query, zone *time.Location) (Page, error) {
	items := []CompactionRecord{}

	rows, err := db.QueryContext(ctx, `SELECT c.id,c.at,c.reason,c.state,c.tokens_before,c.tokens_after,COUNT(r.id),COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0) FROM compactions c LEFT JOIN requests r ON r.compaction_id=c.id WHERE c.scope=? GROUP BY c.id ORDER BY c.at,c.id LIMIT 21 OFFSET ?`, q.Scope, q.Offset)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CompactionRecord
		var at int64
		var before, after sql.NullInt64
		if err := rows.Scan(&c.ID, &at, &c.Reason, &c.State, &before, &after, &c.Requests, &c.Input, &c.Output, &c.Cache); err != nil {
			return Page{}, err
		}
		c.Label = time.UnixMilli(at).In(zone).Format("2006-01-02 15:04")
		c.TokensBefore = nullable(before)
		c.TokensAfter = nullable(after)
		items = append(items, c)
	}
	return page(q.Offset, items), rows.Err()
}
