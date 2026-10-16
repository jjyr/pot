package usage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/jjyr/pot/internal/storage"
)

type Query struct {
	Days     int    `json:"days"`
	Timezone string `json:"timezone"`
	Scope    string `json:"scope"`
	Offset   int    `json:"offset"`
}
type Totals struct {
	Input    int64 `json:"input"`
	Output   int64 `json:"output"`
	Cache    int64 `json:"cache"`
	Requests int64 `json:"requests"`
	Unknown  int64 `json:"unknown"`
}
type Bucket struct {
	Label   string `json:"label"`
	Range   string `json:"range"`
	Input   int64  `json:"input"`
	Output  int64  `json:"output"`
	Cache   int64  `json:"cache"`
	Unknown int64  `json:"unknown"`
}
type AgentModel struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
	Totals
}
type RequestRecord struct {
	ID         string `json:"id"`
	Turn       string `json:"turn"`
	At         int64  `json:"at"`
	Label      string `json:"label"`
	TurnLabel  string `json:"turn_label"`
	Model      string `json:"model"`
	Input      *int64 `json:"input"`
	Output     *int64 `json:"output"`
	Cache      int64  `json:"cache"`
	State      string `json:"state"`
	Agent      string `json:"agent"`
	Provider   string `json:"provider"`
	ProfileID  string `json:"profile_id"`
	Generation *int64 `json:"generation"`
	Purpose    string `json:"purpose"`
}
type ToolTotal struct {
	Name       string `json:"name"`
	Calls      int64  `json:"calls"`
	Failed     int64  `json:"failed"`
	Bytes      int64  `json:"bytes"`
	DurationMS int64  `json:"duration_ms"`
	Unfinished int64  `json:"unfinished"`
}
type CompactionRecord struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Reason       string `json:"reason"`
	State        string `json:"state"`
	TokensBefore *int64 `json:"tokens_before"`
	TokensAfter  *int64 `json:"tokens_after"`
	Totals
}
type Summary struct {
	RoutingTotal         Totals             `json:"routing_total"`
	AgentModels          []AgentModel       `json:"agent_models"`
	AgentModelNextOffset *int               `json:"agent_model_next_offset"`
	Days                 int                `json:"days"`
	Timezone             string             `json:"timezone"`
	Scope                string             `json:"scope"`
	BucketDays           int                `json:"bucket_days"`
	Total                Totals             `json:"total"`
	Lifetime             Totals             `json:"lifetime"`
	Historical           Totals             `json:"historical"`
	Buckets              []Bucket           `json:"buckets"`
	Requests             []RequestRecord    `json:"requests"`
	Tools                []ToolTotal        `json:"tools"`
	Offset               int                `json:"offset"`
	NextOffset           *int               `json:"next_offset"`
	Quota                Quota              `json:"quota"`
	Waiting              *Waiting           `json:"waiting"`
	Compactions          []CompactionRecord `json:"compactions"`
	CompactionTotal      Totals             `json:"compaction_total"`
	CompactionCount      int64              `json:"compaction_count"`
	CompactionNextOffset *int               `json:"compaction_next_offset"`
	Capabilities         map[string]bool    `json:"capabilities"`
}

func aggregate(ctx context.Context, db queryer, scope string, start, end int64, extra string, args ...any) (Totals, error) {
	var t Totals
	parameters := []any{scope, scope, start, end}
	parameters = append(parameters, args...)
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0),COUNT(*),COALESCE(SUM(CASE WHEN r.input IS NULL OR r.output IS NULL THEN 1 ELSE 0 END),0) FROM requests r WHERE (?='' OR r.scope=?) AND r.at>=? AND r.at<? `+extra, parameters...).Scan(&t.Input, &t.Output, &t.Cache, &t.Requests, &t.Unknown)
	return t, err
}

type queryDB interface {
	queryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (l *Ledger) Summary(ctx context.Context, q Query, atMS int64) (Summary, error) {
	var value Summary
	err := storage.Transaction(ctx, l.db, func(tx *sql.Tx) error { var err error; value, err = summary(ctx, tx, q, atMS); return err })
	return value, err
}
func summary(ctx context.Context, db queryDB, q Query, atMS int64) (Summary, error) {
	if q.Days == 0 {
		q.Days = 7
	}
	if q.Timezone == "" {
		q.Timezone = "Asia/Shanghai"
	}
	if q.Days != 7 && q.Days != 30 && q.Days != 90 && q.Days != 365 || q.Offset < 0 || q.Offset > 1000000 || len(q.Scope) > 128 {
		return Summary{}, fmt.Errorf("invalid usage query")
	}
	zone, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return Summary{}, err
	}
	at := time.UnixMilli(atMS).In(zone)
	y, m, d := at.Date()
	today := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	first := today.AddDate(0, 0, 1-q.Days)
	begin, err := midnight(zone, first)
	if err != nil {
		return Summary{}, err
	}
	end, err := midnight(zone, today.AddDate(0, 0, 1))
	if err != nil {
		return Summary{}, err
	}
	stop := min(end.UnixMilli(), atMS+1)
	s := Summary{Days: q.Days, Timezone: q.Timezone, Scope: q.Scope, Offset: q.Offset, BucketDays: 1, Buckets: []Bucket{}, AgentModels: []AgentModel{}, Requests: []RequestRecord{}, Tools: []ToolTotal{}, Compactions: []CompactionRecord{}, Capabilities: map[string]bool{"compaction": true, "provider_quota": false}}
	if q.Days == 365 {
		s.BucketDays = 7
	}
	if s.Total, err = aggregate(ctx, db, q.Scope, begin.UnixMilli(), stop, ""); err != nil {
		return s, err
	}
	if s.Lifetime, err = aggregate(ctx, db, q.Scope, 0, math.MaxInt64, ""); err != nil {
		return s, err
	}
	if s.Historical, err = aggregate(ctx, db, q.Scope, 0, 1, ""); err != nil {
		return s, err
	}
	for offset := 0; offset < q.Days; offset += s.BucketDays {
		a := first.AddDate(0, 0, offset)
		b := first.AddDate(0, 0, min(q.Days, offset+s.BucketDays))
		start, err := midnight(zone, a)
		if err != nil {
			return s, err
		}
		end, err := midnight(zone, b)
		if err != nil {
			return s, err
		}
		t, err := aggregate(ctx, db, q.Scope, start.UnixMilli(), min(end.UnixMilli(), atMS+1), "")
		if err != nil {
			return s, err
		}
		s.Buckets = append(s.Buckets, Bucket{a.Format("01-02"), a.Format("2006-01-02") + " — " + b.AddDate(0, 0, -1).Format("2006-01-02"), t.Input, t.Output, t.Cache, t.Unknown})
	}
	groupStart, groupEnd := begin.UnixMilli(), stop
	if q.Scope != "" {
		groupStart = 0
		groupEnd = math.MaxInt64
	}
	if s.RoutingTotal, err = aggregate(ctx, db, q.Scope, groupStart, groupEnd, "AND r.scope='main' AND EXISTS(SELECT 1 FROM routing_turns t WHERE t.turn_id=r.turn_id)"); err != nil {
		return s, err
	}
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(t.agent,'unknown'),r.model,COUNT(*),COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0),SUM(CASE WHEN r.input IS NULL OR r.output IS NULL THEN 1 ELSE 0 END) FROM requests r LEFT JOIN request_routes t ON t.request_id=r.id WHERE (?='' OR r.scope=?) AND r.at>=? AND r.at<? GROUP BY COALESCE(t.agent,'unknown'),r.model ORDER BY COALESCE(SUM(r.input),0)+COALESCE(SUM(r.output),0) DESC,1,2 LIMIT 21 OFFSET ?`, q.Scope, q.Scope, groupStart, groupEnd, q.Offset)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var a AgentModel
		if err := rows.Scan(&a.Agent, &a.Model, &a.Requests, &a.Input, &a.Output, &a.Cache, &a.Unknown); err != nil {
			rows.Close()
			return s, err
		}
		s.AgentModels = append(s.AgentModels, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	if len(s.AgentModels) > 20 {
		s.AgentModels = s.AgentModels[:20]
		n := q.Offset + 20
		s.AgentModelNextOffset = &n
	}
	if q.Scope != "" {
		if err = summaryDetails(ctx, db, q, zone, &s); err != nil {
			return s, err
		}
	}
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM compactions WHERE scope=?", q.Scope).Scan(&s.CompactionCount); err != nil {
		return s, err
	}
	if s.CompactionTotal, err = aggregate(ctx, db, q.Scope, 0, math.MaxInt64, "AND r.scope=? AND r.compaction_id IS NOT NULL", q.Scope); err != nil {
		return s, err
	}
	if int64(q.Offset+20) < s.Lifetime.Requests {
		n := q.Offset + 20
		s.NextOffset = &n
	}
	if int64(q.Offset+20) < s.CompactionCount {
		n := q.Offset + 20
		s.CompactionNextOffset = &n
	}
	if s.Quota, err = quotaIn(ctx, db, "", atMS); err != nil {
		return s, err
	}
	s.Waiting, err = waitingIn(ctx, db, q.Scope)
	return s, err
}
func summaryDetails(ctx context.Context, db queryDB, q Query, zone *time.Location, s *Summary) error {
	rows, err := db.QueryContext(ctx, `SELECT r.id,r.turn_id,r.at,r.model,r.input,r.output,r.cache,r.state,r.compaction_id,COALESCE(t.agent,'unknown'),COALESCE(t.provider,'unknown'),COALESCE(t.profile_id,''),t.generation FROM requests r LEFT JOIN request_routes t ON t.request_id=r.id WHERE r.scope=? ORDER BY r.at DESC,r.id DESC LIMIT 20 OFFSET ?`, q.Scope, q.Offset)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r RequestRecord
		var input, output, generation sql.NullInt64
		var compaction sql.NullString
		if err := rows.Scan(&r.ID, &r.Turn, &r.At, &r.Model, &input, &output, &r.Cache, &r.State, &compaction, &r.Agent, &r.Provider, &r.ProfileID, &generation); err != nil {
			rows.Close()
			return err
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
		s.Requests = append(s.Requests, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, "SELECT turn_id,DENSE_RANK() OVER (ORDER BY MIN(at),turn_id) FROM requests WHERE scope=? AND at>0 GROUP BY turn_id", q.Scope)
	if err != nil {
		return err
	}
	labels := map[string]string{}
	for rows.Next() {
		var turn string
		var n int64
		if err := rows.Scan(&turn, &n); err != nil {
			rows.Close()
			return err
		}
		labels[turn] = fmt.Sprintf("第 %d 轮", n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i := range s.Requests {
		s.Requests[i].TurnLabel = labels[s.Requests[i].Turn]
		if s.Requests[i].TurnLabel == "" {
			s.Requests[i].TurnLabel = "历史未分类"
		}
	}
	rows, err = db.QueryContext(ctx, `SELECT name,COUNT(*),SUM(failed),COALESCE(SUM(bytes),0),COALESCE(SUM(MAX(end_at-at,0)),0),SUM(CASE WHEN end_at IS NULL THEN 1 ELSE 0 END) FROM tools WHERE scope=? GROUP BY name ORDER BY COALESCE(SUM(bytes),0) DESC,name`, q.Scope)
	if err != nil {
		return err
	}
	for rows.Next() {
		var t ToolTotal
		if err := rows.Scan(&t.Name, &t.Calls, &t.Failed, &t.Bytes, &t.DurationMS, &t.Unfinished); err != nil {
			rows.Close()
			return err
		}
		s.Tools = append(s.Tools, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, `SELECT c.id,c.at,c.reason,c.state,c.tokens_before,c.tokens_after,COUNT(r.id),COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0),COALESCE(SUM(CASE WHEN r.id IS NOT NULL AND (r.input IS NULL OR r.output IS NULL) THEN 1 ELSE 0 END),0) FROM compactions c LEFT JOIN requests r ON r.compaction_id=c.id WHERE c.scope=? GROUP BY c.id ORDER BY c.at DESC,c.id DESC LIMIT 20 OFFSET ?`, q.Scope, q.Offset)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c CompactionRecord
		var at int64
		var before, after sql.NullInt64
		if err := rows.Scan(&c.ID, &at, &c.Reason, &c.State, &before, &after, &c.Requests, &c.Input, &c.Output, &c.Cache, &c.Unknown); err != nil {
			return err
		}
		c.Label = time.UnixMilli(at).In(zone).Format("2006-01-02 15:04")
		c.TokensBefore = nullable(before)
		c.TokensAfter = nullable(after)
		s.Compactions = append(s.Compactions, c)
	}
	return rows.Err()
}
func nullable(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}
