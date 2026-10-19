package usage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func addTotals(target *Totals, value Totals) {
	target.Input += value.Input
	target.Output += value.Output
	target.Cache += value.Cache
	target.Requests += value.Requests
}

func agentTotals(values map[string]*AgentTotal) []AgentTotal {
	rows := make([]AgentTotal, 0, len(values))
	for _, value := range values {
		rows = append(rows, *value)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Input+a.Output != b.Input+b.Output {
			return a.Input+a.Output > b.Input+b.Output
		}
		return a.Agent < b.Agent
	})
	return rows
}

// Aggregate calendar dates in SQLite with explicit IANA-zone boundaries. This
// keeps DST/half-hour zones correct without loading individual request records.
func calendarSummary(ctx context.Context, db queryDB, q Query, first time.Time, zone *time.Location, atMS int64, result *Summary) error {
	periods := make([]string, q.Days)
	args := make([]any, 0, q.Days*2+2)
	result.Buckets = make([]Bucket, q.Days)
	dailyAgents := make([]map[string]*AgentTotal, q.Days)
	for i := range q.Days {
		date := first.AddDate(0, 0, i)
		start, err := midnight(zone, date)
		if err != nil {
			return err
		}
		end, err := midnight(zone, date.AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		periods[i] = fmt.Sprintf("(%d,?,?)", i)
		args = append(args, start.UnixMilli(), min(end.UnixMilli(), atMS+1))
		result.Buckets[i] = Bucket{Date: date.Format("2006-01-02"), Agents: []AgentTotal{}}
		dailyAgents[i] = map[string]*AgentTotal{}
	}
	args = append(args, q.Scope, q.Scope)
	rows, err := db.QueryContext(ctx, `WITH periods(slot,start,stop) AS (VALUES `+strings.Join(periods, ",")+`)
SELECT p.slot,COALESCE(t.agent,'unknown'),r.model,COUNT(*),COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0)
FROM periods p JOIN requests r ON r.at>=p.start AND r.at<p.stop
LEFT JOIN request_routes t ON t.request_id=r.id
WHERE (?='' OR r.scope=?) GROUP BY p.slot,COALESCE(t.agent,'unknown'),r.model`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	agents, models := map[string]*AgentTotal{}, map[string]*ModelTotal{}
	for rows.Next() {
		var slot int
		var agent, model string
		var value Totals
		if err := rows.Scan(&slot, &agent, &model, &value.Requests, &value.Input, &value.Output, &value.Cache); err != nil {
			return err
		}
		addTotals(&result.Total, value)
		addTotals(&result.Buckets[slot].Totals, value)
		if dailyAgents[slot][agent] == nil {
			dailyAgents[slot][agent] = &AgentTotal{Agent: agent}
		}
		addTotals(&dailyAgents[slot][agent].Totals, value)
		if agents[agent] == nil {
			agents[agent] = &AgentTotal{Agent: agent}
		}
		addTotals(&agents[agent].Totals, value)
		if models[model] == nil {
			models[model] = &ModelTotal{Model: model}
		}
		addTotals(&models[model].Totals, value)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range result.Buckets {
		result.Buckets[i].Agents = agentTotals(dailyAgents[i])
	}
	result.Agents = agentTotals(agents)
	result.Models = make([]ModelTotal, 0, len(models))
	for _, model := range models {
		result.Models = append(result.Models, *model)
	}
	sort.Slice(result.Models, func(i, j int) bool {
		a, b := result.Models[i], result.Models[j]
		if a.Input+a.Output != b.Input+b.Output {
			return a.Input+a.Output > b.Input+b.Output
		}
		return a.Model < b.Model
	})
	return nil
}
