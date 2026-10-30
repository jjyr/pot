package usage

import "context"

type ToolRecord struct {
	ID           string `json:"id"`
	Scope        string `json:"scope"`
	Turn         string `json:"turn"`
	Name         string `json:"name"`
	Agent        string `json:"agent"`
	At           int64  `json:"at"`
	EndAt        *int64 `json:"end_at"`
	Failed       bool   `json:"failed"`
	CallTokens   *int64 `json:"call_estimated_tokens"`
	ResultTokens *int64 `json:"result_estimated_tokens"`
}

func (l *Ledger) ToolRecords(ctx context.Context, q Query, at int64) (Page, error) {
	start, stop, _, _, err := Bounds(q, at)
	if err != nil {
		return Page{}, err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT json_extract(id,'$[2]'),scope,turn_id,name,agent,at,end_at,failed,call_estimated_tokens,result_estimated_tokens FROM tools WHERE (?='' OR scope=?) AND at>=? AND at<? AND (?='' OR turn_id=?) ORDER BY at DESC,id DESC LIMIT 21 OFFSET ?`, q.Scope, q.Scope, start, stop, q.Turn, q.Turn, q.Offset)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	items := []ToolRecord{}
	for rows.Next() {
		var r ToolRecord
		if err = rows.Scan(&r.ID, &r.Scope, &r.Turn, &r.Name, &r.Agent, &r.At, &r.EndAt, &r.Failed, &r.CallTokens, &r.ResultTokens); err != nil {
			return Page{}, err
		}
		items = append(items, r)
	}
	return page(q.Offset, items), rows.Err()
}
