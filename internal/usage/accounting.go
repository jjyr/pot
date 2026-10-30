package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/jjyr/pot/pkg/storage"
)

type AccountingQuery struct {
	Details bool `json:"details"`
	Query
	Assistant string `json:"assistant"`
	Task      string `json:"task"`
	Request   string `json:"request"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	GroupBy   string `json:"group_by"`
}
type CostTotal struct {
	CacheWrite      int64 `json:"cache_write"`
	CacheWriteKnown bool  `json:"cache_write_known"`
	Reasoning       int64 `json:"reasoning"`
	ReasoningKnown  bool  `json:"reasoning_known"`
	MeasuredTotals
	USD                string `json:"usd"`
	Priced             int64  `json:"priced_requests"`
	Unpriced           int64  `json:"unpriced_requests"`
	MissingUsage       int64  `json:"missing_usage_requests"`
	UnknownAttribution int64  `json:"unknown_attribution_requests"`
	Complete           bool   `json:"complete"`
}
type CostGroup struct {
	Key      string `json:"key"`
	Provider string `json:"provider,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Model    string `json:"model,omitempty"`
	CostTotal
}
type CostRecord struct {
	ID    string `json:"id"`
	Turn  string `json:"turn"`
	At    int64  `json:"at"`
	State string `json:"state"`
	Attribution
	Provider   string  `json:"provider"`
	Profile    string  `json:"profile"`
	Model      string  `json:"model"`
	Input      *int64  `json:"input"`
	Output     *int64  `json:"output"`
	Cache      *int64  `json:"cache"`
	CacheWrite *int64  `json:"cache_write"`
	Reasoning  *int64  `json:"reasoning"`
	Price      *Price  `json:"price"`
	USD        *string `json:"usd"`
	CostStatus string  `json:"cost_status"`
}
type Accounting struct {
	Records    []CostRecord `json:"records,omitempty"`
	NextOffset *int         `json:"next_offset,omitempty"`
	Total      CostTotal    `json:"total"`
	GroupBy    string       `json:"group_by"`
	Groups     []CostGroup  `json:"groups"`
}

func (q AccountingQuery) Validate() error {
	switch q.GroupBy {
	case "", "model", "stage", "assistant", "task", "date":
	default:
		return errors.New("invalid usage grouping")
	}
	for _, s := range []string{q.Assistant, q.Task, q.Request, q.Provider, q.Model} {
		if len(s) > 256 {
			return errors.New("usage filter too long")
		}
	}
	return nil
}
func (l *Ledger) Accounting(ctx context.Context, q AccountingQuery, at int64) (Accounting, error) {
	var result Accounting
	if e := q.Validate(); e != nil {
		return result, e
	}
	start, stop, _, _, e := Bounds(q.Query, at)
	if e != nil {
		return result, e
	}
	if q.GroupBy == "" {
		q.GroupBy = "model"
	}
	zone := q.Timezone
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	location, _ := time.LoadLocation(zone)
	result.GroupBy = q.GroupBy
	result.Groups = []CostGroup{}
	e = storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, `SELECT r.input,r.output,COALESCE(a.cache_read,CASE WHEN r.cache_reported=1 THEN r.cache END),COALESCE(rt.provider,''),COALESCE(rt.profile_id,''),r.model,COALESCE(a.assistant_id,''),COALESCE(a.task_id,''),CASE WHEN r.compaction_id IS NOT NULL THEN 'compaction' ELSE COALESCE(a.stage,'unknown') END,r.at,a.cost_usd,COALESCE(a.cost_status,'unpriced'),r.id,r.turn_id,r.state,COALESCE(a.correlation_id,''),COALESCE(a.effort,''),a.price,a.cache_write,a.reasoning
 FROM requests r LEFT JOIN request_routes rt ON rt.request_id=r.id LEFT JOIN request_accounting a ON a.request_id=r.id
 WHERE r.state NOT IN ('rejected','pending') AND r.at>=? AND r.at<?
 AND (?='' OR a.assistant_id=?) AND (?='' OR a.task_id=?) AND (?='' OR a.correlation_id=?)
 AND (?='' OR r.turn_id=?) AND (?='' OR rt.provider=?) AND (?='' OR r.model=?) ORDER BY r.at DESC,r.id DESC`, start, stop, q.Assistant, q.Assistant, q.Task, q.Task, q.Request, q.Request, q.Turn, q.Turn, q.Provider, q.Provider, q.Model, q.Model)
		if e != nil {
			return e
		}
		defer rows.Close()
		groups := map[string]*CostGroup{}
		result.Total = newCostTotal()
		for rows.Next() {
			var in, out, cache sql.NullInt64
			var provider, profile, model, assistant, task, stage, status string
			var stamp int64
			var cost, price sql.NullString
			var record CostRecord
			var cw, reasoning sql.NullInt64
			if e = rows.Scan(&in, &out, &cache, &provider, &profile, &model, &assistant, &task, &stage, &stamp, &cost, &status, &record.ID, &record.Turn, &record.State, &record.Request, &record.Effort, &price, &cw, &reasoning); e != nil {
				return e
			}
			if q.Details && result.Total.Requests >= int64(q.Offset) && len(result.Records) < 20 {
				record.At = stamp
				record.Assistant = assistant
				record.Task = task
				record.Stage = stage
				record.Provider = provider
				record.Profile = profile
				record.Model = model
				record.CostStatus = status
				ptr := func(v sql.NullInt64) *int64 {
					if !v.Valid {
						return nil
					}
					return &v.Int64
				}
				record.Input = ptr(in)
				record.Output = ptr(out)
				record.Cache = ptr(cache)
				record.CacheWrite = ptr(cw)
				record.Reasoning = ptr(reasoning)
				if cost.Valid {
					record.USD = &cost.String
				}
				if price.Valid {
					if e = json.Unmarshal([]byte(price.String), &record.Price); e != nil {
						return e
					}
				}
				result.Records = append(result.Records, record)
			}
			key := ""
			switch q.GroupBy {
			case "model":
				key = provider + " / " + profile + " / " + model
			case "stage":
				key = stage
			case "assistant":
				key = assistant
			case "task":
				key = task
				if key == "" && assistant != "" {
					key = "assistant (no task)"
				}
			case "date":
				key = time.UnixMilli(stamp).In(location).Format(time.DateOnly)
			}
			groupID := key
			if q.GroupBy == "model" {
				b, _ := json.Marshal([]string{provider, profile, model})
				groupID = string(b)
			}
			if q.GroupBy == "task" {
				b, _ := json.Marshal([]string{task, fmt.Sprint(task == "" && assistant != "")})
				groupID = string(b)
			}
			if key == "" {
				key = "unknown"
			}
			g := groups[groupID]
			if g == nil {
				g = &CostGroup{Key: key, CostTotal: newCostTotal()}
				if q.GroupBy == "model" {
					g.Provider = provider
					g.Profile = profile
					g.Model = model
				}
				groups[groupID] = g
			}
			for _, t := range []*CostTotal{&result.Total, &g.CostTotal} {
				t.Requests++
				t.CacheWrite += cw.Int64
				t.Reasoning += reasoning.Int64
				t.CacheWriteKnown = t.CacheWriteKnown && cw.Valid
				t.ReasoningKnown = t.ReasoningKnown && reasoning.Valid
				t.Input += in.Int64
				t.Output += out.Int64
				t.Cache += cache.Int64
				t.InputKnown = t.InputKnown && in.Valid
				t.OutputKnown = t.OutputKnown && out.Valid
				t.CacheKnown = t.CacheKnown && cache.Valid
				if assistant == "" {
					t.UnknownAttribution++
				}
				switch status {
				case "priced":
					if !cost.Valid {
						return errors.New("priced record without amount")
					}
					a, ok := new(big.Rat).SetString(t.USD)
					if !ok {
						return errors.New("invalid stored cost")
					}
					b, ok := new(big.Rat).SetString(cost.String)
					if !ok {
						return errors.New("invalid stored cost")
					}
					t.USD = a.Add(a, b).FloatString(15)
					t.Priced++
				case "usage_missing":
					t.MissingUsage++
				default:
					t.Unpriced++
				}
				t.Complete = t.Unpriced == 0 && t.MissingUsage == 0
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if q.Details && result.Total.Requests > int64(q.Offset+len(result.Records)) {
			next := q.Offset + len(result.Records)
			result.NextOffset = &next
		}
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			result.Groups = append(result.Groups, *groups[k])
		}
		return nil
	})
	return result, e
}
func newCostTotal() CostTotal {
	return CostTotal{MeasuredTotals: MeasuredTotals{InputKnown: true, OutputKnown: true, CacheKnown: true}, USD: "0.000000000000000", Complete: true, CacheWriteKnown: true, ReasoningKnown: true}
}
