package usage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jjyr/pot/pkg/storage"
)

type MeasuredTotals struct {
	Totals
	InputKnown  bool `json:"input_known"`
	OutputKnown bool `json:"output_known"`
	CacheKnown  bool `json:"cache_known"`
}

type StageTotal struct {
	Agent string `json:"agent"`
	Stage string `json:"stage"`
	MeasuredTotals
}

type ActivityTools struct {
	Calls                 int64 `json:"calls"`
	Failed                int64 `json:"failed"`
	Unfinished            int64 `json:"unfinished"`
	DurationMS            int64 `json:"duration_ms"`
	ArgumentBytes         int64 `json:"argument_bytes"`
	ResultBytes           int64 `json:"result_bytes"`
	CallEstimatedTokens   int64 `json:"call_estimated_tokens"`
	ResultEstimatedTokens int64 `json:"result_estimated_tokens"`
	CallsKnown            bool  `json:"calls_known"`
	ResultsKnown          bool  `json:"results_known"`
}

type ToolEstimateGroup struct {
	Name  string `json:"name"`
	Agent string `json:"agent"`
	ActivityTools
}

type Breakdown struct {
	Start      string              `json:"start"`
	End        string              `json:"end"`
	Timezone   string              `json:"timezone"`
	Scope      string              `json:"scope"`
	Models     []StageTotal        `json:"models"`
	Tools      ActivityTools       `json:"tools"`
	ToolGroups []ToolEstimateGroup `json:"tool_groups"`
}

func Bounds(q Query, atMS int64) (start, stop int64, first, last string, err error) {
	if q.Offset < 0 || q.Offset > 1000000 || len(q.Scope) > 128 || len(q.Turn) > 128 {
		return 0, 0, "", "", fmt.Errorf("invalid usage query")
	}
	if q.Timezone == "" {
		q.Timezone = "Asia/Shanghai"
	}
	zone, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return 0, 0, "", "", err
	}
	at := time.UnixMilli(atMS).In(zone)
	y, m, d := at.Date()
	today := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	f, l, err := queryDates(q, today)
	if err != nil {
		return 0, 0, "", "", err
	}
	begin, err := midnight(zone, f)
	if err != nil {
		return 0, 0, "", "", err
	}
	end, err := midnight(zone, l.AddDate(0, 0, 1))
	if err != nil {
		return 0, 0, "", "", err
	}
	stop = min(end.UnixMilli(), atMS+1)
	return begin.UnixMilli(), stop, f.Format(time.DateOnly), l.Format(time.DateOnly), nil
}

func (l *Ledger) Breakdown(ctx context.Context, q Query, atMS int64) (Breakdown, error) {
	var value Breakdown
	err := storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		var err error
		value, err = breakdown(ctx, tx, q, atMS)
		return err
	})
	return value, err
}

func breakdown(ctx context.Context, db queryDB, q Query, atMS int64) (Breakdown, error) {
	start, stop, first, last, err := Bounds(q, atMS)
	if err != nil {
		return Breakdown{}, err
	}
	if q.Timezone == "" {
		q.Timezone = "Asia/Shanghai"
	}
	value := Breakdown{Start: first, End: last, Timezone: q.Timezone, Scope: q.Scope, Models: []StageTotal{}}
	value.Models, err = stageTotals(ctx, db, q, start, stop)
	if err != nil {
		return value, err
	}
	value.Tools, err = activityTools(ctx, db, q, start, stop)
	if err != nil {
		return value, err
	}
	value.ToolGroups, err = toolEstimateGroups(ctx, db, q, start, stop)
	return value, err
}

const agentRoleExpr = `CASE WHEN rt.agent IN ('pot','summary','main') THEN 'main' WHEN rt.agent IN ('task','worker') THEN 'task' ELSE COALESCE(rt.agent,'unknown') END`

const stageExpr = `CASE WHEN r.compaction_id IS NOT NULL THEN 'compaction'
WHEN substr(r.turn_id,1,11)='pot-result-' THEN 'background_update'
ELSE 'conversation' END`

func stageTotals(ctx context.Context, db queryDB, q Query, start, stop int64) ([]StageTotal, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+agentRoleExpr+`,`+stageExpr+`,
COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0),COUNT(*),
MIN(r.input IS NOT NULL),MIN(r.output IS NOT NULL),MIN(r.input IS NOT NULL AND r.cache_reported=1)
FROM requests r LEFT JOIN request_routes rt ON rt.request_id=r.id
WHERE (?='' OR r.scope=?) AND r.at>=? AND r.at<? AND r.state<>'rejected' AND (?='' OR r.turn_id=?)
GROUP BY `+agentRoleExpr+`,`+stageExpr+` ORDER BY 1,2`,
		q.Scope, q.Scope, start, stop, q.Turn, q.Turn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := []StageTotal{}
	for rows.Next() {
		var value StageTotal
		if err := rows.Scan(&value.Agent, &value.Stage, &value.Input, &value.Output, &value.Cache, &value.Requests,
			&value.InputKnown, &value.OutputKnown, &value.CacheKnown); err != nil {
			return nil, err
		}
		totals = append(totals, value)
	}
	return totals, rows.Err()
}

const toolAggregateColumns = `COUNT(*),COALESCE(SUM(failed),0),
 COALESCE(SUM(end_at IS NULL),0),COALESCE(SUM(MAX(end_at-at,0)),0),
 COALESCE(SUM(argument_bytes),0),COALESCE(SUM(bytes),0),
 COALESCE(SUM(call_estimated_tokens),0),COALESCE(SUM(result_estimated_tokens),0),
 COALESCE(MIN(call_estimated_tokens IS NOT NULL),0),COALESCE(MIN(result_estimated_tokens IS NOT NULL),0)`
const toolAggregateFilter = ` FROM tools WHERE (?='' OR scope=?) AND at>=? AND at<? AND (?='' OR turn_id=?)`

func toolAggregateDest(v *ActivityTools) []any {
	return []any{&v.Calls, &v.Failed, &v.Unfinished, &v.DurationMS, &v.ArgumentBytes, &v.ResultBytes, &v.CallEstimatedTokens, &v.ResultEstimatedTokens, &v.CallsKnown, &v.ResultsKnown}
}
func activityTools(ctx context.Context, db queryDB, q Query, start, stop int64) (ActivityTools, error) {
	var v ActivityTools
	err := db.QueryRowContext(ctx, "SELECT "+toolAggregateColumns+toolAggregateFilter, q.Scope, q.Scope, start, stop, q.Turn, q.Turn).Scan(toolAggregateDest(&v)...)
	return v, err
}
func toolEstimateGroups(ctx context.Context, db queryDB, q Query, start, stop int64) ([]ToolEstimateGroup, error) {
	rows, err := db.QueryContext(ctx, "SELECT name,agent,"+toolAggregateColumns+toolAggregateFilter+` GROUP BY name,agent ORDER BY COALESCE(SUM(call_estimated_tokens),0)+COALESCE(SUM(result_estimated_tokens),0) DESC,name,agent`, q.Scope, q.Scope, start, stop, q.Turn, q.Turn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ToolEstimateGroup{}
	for rows.Next() {
		var v ToolEstimateGroup
		dest := append([]any{&v.Name, &v.Agent}, toolAggregateDest(&v.ActivityTools)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
