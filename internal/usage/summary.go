package usage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/jjyr/pot/pkg/storage"
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
	RoutingTotal Totals          `json:"routing_total"`
	Days         int             `json:"days"`
	Timezone     string          `json:"timezone"`
	Scope        string          `json:"scope"`
	BucketDays   int             `json:"bucket_days"`
	Total        Totals          `json:"total"`
	Lifetime     Totals          `json:"lifetime"`
	Historical   Totals          `json:"historical"`
	Buckets      []Bucket        `json:"buckets"`
	Quota        Quota           `json:"quota"`
	Waiting      *Waiting        `json:"waiting"`
	Capabilities map[string]bool `json:"capabilities"`
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
	s := Summary{Days: q.Days, Timezone: q.Timezone, Scope: q.Scope, BucketDays: 1, Buckets: []Bucket{}, Capabilities: map[string]bool{"compaction": true, "provider_quota": true}}
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
	if s.Quota, err = quotaIn(ctx, db, "", atMS); err != nil {
		return s, err
	}
	s.Waiting, err = waitingIn(ctx, db, q.Scope)
	return s, err
}
func nullable(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}
