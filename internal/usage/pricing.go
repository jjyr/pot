package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
)

// Price rates are decimal USD per million tokens. Empty cache rates mean the
// input rate applies; a distinct cache rate requires a reported cache count.
// Normalized input includes both cache categories; output includes reasoning.
type Price struct {
	Source      string      `json:"source,omitempty"`
	Provider    string      `json:"provider,omitempty"`
	Model       string      `json:"model,omitempty"`
	Match       string      `json:"match,omitempty"`
	UpdatedAt   int64       `json:"updated_at,omitempty"`
	Tiers       []PriceTier `json:"tiers,omitempty"`
	Unsupported string      `json:"unsupported,omitempty"`
	Version     string      `json:"version"`
	Input       string      `json:"input"`
	Output      string      `json:"output"`
	CacheRead   string      `json:"cache_read,omitempty"`
	CacheWrite  string      `json:"cache_write,omitempty"`
}

var ratePattern = regexp.MustCompile(`^[0-9]{1,7}(\.[0-9]{1,9})?$`)

func parseRate(s string) (*big.Rat, error) {
	if !ratePattern.MatchString(s) {
		return nil, errors.New("invalid decimal rate")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Cmp(big.NewRat(1000000, 1)) > 0 {
		return nil, errors.New("rate exceeds limit")
	}
	return r, nil
}
func (p Price) Validate() error {
	if p.Version == "" || len(p.Version) > 256 {
		return errors.New("price version required")
	}
	for _, s := range []string{p.Input, p.Output} {
		if _, e := parseRate(s); e != nil {
			return e
		}
	}
	for _, s := range []string{p.CacheRead, p.CacheWrite} {
		if s != "" {
			if _, e := parseRate(s); e != nil {
				return e
			}
		}
	}
	last := int64(0)
	for _, t := range p.Tiers {
		if t.Above <= last || t.Above > MaxSafeInteger {
			return errors.New("invalid context price tier")
		}
		last = t.Above
		if e := (Price{Version: p.Version, Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite}).Validate(); e != nil {
			return e
		}
	}
	return nil
}
func Cost(p Price, input, output, read, write *int64) (string, error) {
	if p.Unsupported != "" {
		return "", errors.New(p.Unsupported)
	}
	if input != nil {
		for _, t := range p.Tiers {
			if *input > t.Above {
				p.Input = t.Input
				p.Output = t.Output
				p.CacheRead = t.CacheRead
				p.CacheWrite = t.CacheWrite
			}
		}
	}
	if e := p.Validate(); e != nil {
		return "", e
	}
	if input == nil || output == nil {
		return "", errors.New("missing input/output usage")
	}
	for _, n := range []*int64{input, output, read, write} {
		if n != nil && (*n < 0 || *n > MaxSafeInteger) {
			return "", errors.New("invalid token count")
		}
	}
	in, _ := parseRate(p.Input)
	out, _ := parseRate(p.Output)
	total := new(big.Rat).Mul(big.NewRat(*input, 1), in)
	total.Add(total, new(big.Rat).Mul(big.NewRat(*output, 1), out))
	var cached int64
	for i, s := range []string{p.CacheRead, p.CacheWrite} {
		n := []*int64{read, write}[i]
		if n != nil {
			cached += *n
		}
		if s == "" {
			continue
		}
		rate, _ := parseRate(s)
		delta := new(big.Rat).Sub(rate, in)
		if n == nil {
			if delta.Sign() != 0 {
				return "", errors.New("missing cache split")
			}
			continue
		}
		total.Add(total, new(big.Rat).Mul(big.NewRat(*n, 1), delta))
	}
	if cached > *input {
		return "", errors.New("cache exceeds input")
	}
	return total.Quo(total, big.NewRat(1000000, 1)).FloatString(15), nil
}

func reserveAccounting(ctx context.Context, tx *sql.Tx, r ReserveRequest) error {
	a := Attribution{Stage: "unknown"}
	if r.Attribution != nil {
		a = *r.Attribution
	}
	if a.Stage == "" {
		a.Stage = "unknown"
	}
	var price any
	status := "unpriced"
	if r.Price != nil {
		raw, e := json.Marshal(r.Price)
		if e != nil {
			return e
		}
		price = string(raw)
		if r.Price.Unsupported == "" {
			if e = r.Price.Validate(); e != nil {
				return e
			}
			status = "usage_missing"
		}
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO request_accounting(request_id,assistant_id,task_id,correlation_id,stage,effort,price,cost_status) VALUES(?,?,?,?,?,?,?,?)`, r.ID, a.Assistant, a.Task, a.Request, a.Stage, a.Effort, price, status)
	return e
}
func settleAccounting(ctx context.Context, tx *sql.Tx, id string, frame Frame) error {
	write, reasoning := frame.CacheWrite, frame.Reasoning
	if frame.Cache != nil {
		if _, e := tx.ExecContext(ctx, `UPDATE request_accounting SET cache_read=? WHERE request_id=?`, *frame.Cache, id); e != nil {
			return e
		}
	}
	if reasoning != nil {
		if _, e := tx.ExecContext(ctx, `UPDATE request_accounting SET reasoning=? WHERE request_id=?`, *reasoning, id); e != nil {
			return e
		}
	}
	if write != nil {
		if _, e := tx.ExecContext(ctx, `UPDATE request_accounting SET cache_write=? WHERE request_id=?`, *write, id); e != nil {
			return e
		}
	}
	var raw sql.NullString
	var input, output, read, cw sql.NullInt64
	e := tx.QueryRowContext(ctx, `SELECT a.price,r.input,r.output,a.cache_read,a.cache_write FROM requests r JOIN request_accounting a ON a.request_id=r.id WHERE r.id=?`, id).Scan(&raw, &input, &output, &read, &cw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if !raw.Valid {
		return nil
	}
	var p Price
	if e = json.Unmarshal([]byte(raw.String), &p); e != nil {
		return e
	}
	ptr := func(n sql.NullInt64) *int64 {
		if !n.Valid {
			return nil
		}
		return &n.Int64
	}
	amount, e := Cost(p, ptr(input), ptr(output), ptr(read), ptr(cw))
	status := "priced"
	var cost any = amount
	if e != nil {
		status = "usage_missing"
		if p.Unsupported != "" {
			status = "unpriced"
		}
		cost = nil
	}
	_, e = tx.ExecContext(ctx, `UPDATE request_accounting SET cost_usd=?,cost_status=? WHERE request_id=?`, cost, status, id)
	return e
}
