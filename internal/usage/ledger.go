// Package usage is the shared durable metering and allowance ledger. It never
// performs networking, schedules Agents or assumes missing usage means zero.
package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
	_ "time/tzdata"

	"github.com/jjyr/pot/pkg/storage"
)

const MaxSafeInteger int64 = 9007199254740991

type Policy struct {
	Period     string `json:"period"`
	Limit      int64  `json:"limit"`
	Timezone   string `json:"timezone"`
	AutoResume bool   `json:"auto_resume"`
}

func DefaultPolicy() Policy { return Policy{"none", 2000000, "Asia/Shanghai", true} }
func (p Policy) Validate() error {
	if p.Period != "none" && p.Period != "day" && p.Period != "week" {
		return errors.New("invalid allowance period")
	}
	if p.Limit < 1 || p.Limit > MaxSafeInteger {
		return errors.New("invalid token limit")
	}
	if p.Timezone == "" || p.Timezone == "Local" {
		return errors.New("allowance timezone must be an explicit IANA zone")
	}
	_, err := time.LoadLocation(p.Timezone)
	return err
}
func (p Policy) Window(atMS int64) (int64, int64, error) {
	if err := p.Validate(); err != nil {
		return 0, 0, err
	}
	zone, _ := time.LoadLocation(p.Timezone)
	at := time.UnixMilli(atMS).In(zone)
	year, month, day := at.Date()
	date := time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
	if p.Period == "week" {
		date = date.AddDate(0, 0, -(int(at.Weekday())+6)%7)
	}
	start, err := midnight(zone, date)
	if err != nil {
		return 0, 0, err
	}
	days := 1
	if p.Period == "week" {
		days = 7
	}
	end, err := midnight(zone, date.AddDate(0, 0, days))
	return start.UnixMilli(), end.UnixMilli(), err
}
func midnight(zone *time.Location, date time.Time) (time.Time, error) {
	y, m, d := date.Date()
	for minute := 0; minute < 1440; minute++ {
		candidate := time.Date(y, m, d, minute/60, minute%60, 0, 0, zone)
		cy, cm, cd := candidate.Date()
		if cy == y && cm == m && cd == d && candidate.Hour() == minute/60 && candidate.Minute() == minute%60 {
			earliest := candidate
			for delta := time.Minute; delta <= 3*time.Hour; delta += time.Minute {
				prior := candidate.Add(-delta)
				py, pm, pd := prior.Date()
				if py == y && pm == m && pd == d && prior.Hour() == minute/60 && prior.Minute() == minute%60 {
					earliest = prior
				}
			}
			return earliest, nil
		}
	}
	return time.Time{}, errors.New("date does not exist in timezone")
}

type Route struct {
	Provider   string `json:"provider"`
	Generation int64  `json:"generation"`
	SpecHash   string `json:"spec_hash"`
	Agent      string `json:"agent"`
	ProfileID  string `json:"profile_id"`
}
type ReserveRequest struct {
	ID            string
	Scope         string
	Turn          string
	AtMS          int64
	Model         string
	InputEstimate int64
	MaxOutput     int64
	Route         *Route
}
type Reservation struct {
	OutputCap int64 `json:"output_cap"`
	Duplicate bool  `json:"duplicate"`
}
type Frame struct {
	Input  *int64
	Output *int64
	Cache  *int64
}
type Quota struct {
	Provider   string `json:"provider,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
	Policy     Policy `json:"policy"`
	Active     bool   `json:"active"`
	Used       int64  `json:"used"`
	Reserved   int64  `json:"reserved"`
	Remaining  int64  `json:"remaining"`
	ResetAt    int64  `json:"reset_at"`
	ResetLabel string `json:"reset_label,omitempty"`
}
type Ledger struct{ db *sql.DB }

func Open(path string) (*Ledger, error) {
	db, err := storage.OpenDatabase(path, storage.UsageDatabase)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`UPDATE requests SET state='unknown' WHERE state IN ('pending','started');
UPDATE compactions SET state='interrupted' WHERE state='running';`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Ledger{db}, nil
}
func (l *Ledger) Close() error { return l.db.Close() }

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func policyIn(ctx context.Context, db queryer, key string) (int64, Policy, error) {
	var body string
	err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, DefaultPolicy(), nil
	}
	if err != nil {
		return 0, Policy{}, err
	}
	var stored struct {
		Revision int64  `json:"revision"`
		Policy   Policy `json:"policy"`
	}
	if err := json.Unmarshal([]byte(body), &stored); err != nil {
		return 0, Policy{}, err
	}
	return stored.Revision, stored.Policy, stored.Policy.Validate()
}
func putPolicy(ctx context.Context, db executor, key string, revision int64, p Policy) error {
	body, err := json.Marshal(struct {
		Revision int64  `json:"revision"`
		Policy   Policy `json:"policy"`
	}{revision, p})
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "INSERT INTO settings VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(body))
	return err
}
func (l *Ledger) Policy(ctx context.Context) (Policy, error) {
	_, p, err := policyIn(ctx, l.db, "policy")
	return p, err
}
func (l *Ledger) SetPolicy(ctx context.Context, p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return putPolicy(ctx, l.db, "policy", 0, p)
}
func (l *Ledger) ProviderPolicy(ctx context.Context, provider string) (int64, Policy, error) {
	return policyIn(ctx, l.db, "provider:"+provider)
}
func (l *Ledger) SetProviderPolicy(ctx context.Context, provider string, revision int64, p Policy) error {
	if provider == "" || len(provider) > 256 {
		return errors.New("invalid provider identity")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if revision < 0 || revision >= MaxSafeInteger {
		return errors.New("provider revision overflow")
	}
	return storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		current, _, err := policyIn(ctx, tx, "provider:"+provider)
		if err != nil {
			return err
		}
		if current != revision {
			return errors.New("provider allowance changed; refresh before saving")
		}
		return putPolicy(ctx, tx, "provider:"+provider, revision+1, p)
	})
}
func quotaIn(ctx context.Context, db queryer, provider string, atMS int64) (Quota, error) {
	key := "policy"
	if provider != "" {
		key = "provider:" + provider
	}
	rev, p, err := policyIn(ctx, db, key)
	q := Quota{Provider: provider, Revision: rev, Policy: p, Active: p.Period != "none"}
	if err != nil || !q.Active {
		return q, err
	}
	start, end, err := p.Window(atMS)
	if err != nil {
		return q, err
	}
	query := "SELECT COALESCE(SUM(COALESCE(r.input,0)+COALESCE(r.output,0)),0),COALESCE(SUM(r.reserved),0) FROM requests r WHERE r.at>=? AND r.at<?"
	args := []any{start, end}
	if provider != "" {
		query = "SELECT COALESCE(SUM(COALESCE(r.input,0)+COALESCE(r.output,0)),0),COALESCE(SUM(r.reserved),0) FROM requests r JOIN request_routes t ON t.request_id=r.id WHERE r.at>=? AND r.at<? AND t.provider=?"
		args = append(args, provider)
	}
	if err := db.QueryRowContext(ctx, query, args...).Scan(&q.Used, &q.Reserved); err != nil {
		return q, err
	}
	q.Remaining = max(int64(0), p.Limit-q.Used-q.Reserved)
	q.ResetAt = end
	zone, _ := time.LoadLocation(p.Timezone)
	q.ResetLabel = time.UnixMilli(end).In(zone).Format("2006-01-02 15:04 MST")
	return q, nil
}
func (l *Ledger) Quota(ctx context.Context, atMS int64) (Quota, error) {
	return quotaIn(ctx, l.db, "", atMS)
}
func (l *Ledger) ProviderQuota(ctx context.Context, provider string, atMS int64) (Quota, error) {
	if provider == "" {
		return Quota{}, errors.New("provider required")
	}
	return quotaIn(ctx, l.db, provider, atMS)
}
func (l *Ledger) Reserve(ctx context.Context, r ReserveRequest) (*Reservation, error) {
	if r.ID == "" || r.Scope == "" || r.Turn == "" || r.Model == "" || r.AtMS < 0 || r.InputEstimate < 0 || r.MaxOutput < 1 || r.InputEstimate > MaxSafeInteger || r.MaxOutput > MaxSafeInteger {
		return nil, errors.New("invalid usage reservation")
	}
	if r.Route != nil && (r.Route.Provider == "" || r.Route.Generation < 1 || r.Route.Agent == "" || r.Route.SpecHash == "") {
		return nil, errors.New("invalid route attribution")
	}
	original, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var result *Reservation
	err = storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		var old string
		var cap int64
		err := tx.QueryRowContext(ctx, "SELECT original,output_cap FROM requests WHERE id=?", r.ID).Scan(&old, &cap)
		if err == nil {
			if old != string(original) {
				return errors.New("usage request ID reused with different content")
			}
			result = &Reservation{cap, true}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		cap = r.MaxOutput
		providers := []string{""}
		if r.Route != nil {
			providers = append(providers, r.Route.Provider)
		}
		for _, provider := range providers {
			q, err := quotaIn(ctx, tx, provider, r.AtMS)
			if err != nil {
				return err
			}
			if q.Active {
				available := q.Remaining - r.InputEstimate
				if available <= 0 {
					return nil
				}
				cap = min(cap, available)
			}
		}
		if r.InputEstimate > math.MaxInt64-cap {
			return errors.New("reservation overflow")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO requests(id,scope,turn_id,at,model,reserved,input_estimate,output_cap,original) VALUES (?,?,?,?,?,?,?,?,?)", r.ID, r.Scope, r.Turn, r.AtMS, r.Model, r.InputEstimate+cap, r.InputEstimate, cap, string(original)); err != nil {
			return err
		}
		if route := r.Route; route != nil {
			if _, err := tx.ExecContext(ctx, "INSERT INTO request_routes VALUES (?,?,?,?,?,?)", r.ID, route.Provider, route.Generation, route.SpecHash, route.Agent, route.ProfileID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM waits WHERE scope=?", r.Scope); err != nil {
			return err
		}
		result = &Reservation{cap, false}
		return nil
	})
	return result, err
}
func (l *Ledger) ReserveRouted(ctx context.Context, r ReserveRequest) (*Reservation, error) {
	if r.Route == nil {
		return nil, errors.New("routed reservation requires route")
	}
	return l.Reserve(ctx, r)
}

// Claim durably crosses the network-attempt boundary exactly once. A false
// result fences sending, including unknown requests retained after a restart.
func (l *Ledger) Claim(ctx context.Context, id string) (bool, error) {
	result, err := l.db.ExecContext(ctx, "UPDATE requests SET state='started' WHERE id=? AND state='pending'", id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (l *Ledger) Settle(ctx context.Context, id string, u Frame) error {
	for _, n := range []*int64{u.Input, u.Output, u.Cache} {
		if n != nil && (*n < 0 || *n > MaxSafeInteger) {
			return errors.New("invalid provider usage")
		}
	}
	return storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		var input, output sql.NullInt64
		var cache int64
		if err := tx.QueryRowContext(ctx, "SELECT input,output,cache FROM requests WHERE id=?", id).Scan(&input, &output, &cache); err != nil {
			return err
		}
		if u.Input != nil {
			input = sql.NullInt64{Int64: *u.Input, Valid: true}
		}
		if u.Output != nil {
			output = sql.NullInt64{Int64: *u.Output, Valid: true}
		}
		if u.Cache != nil {
			cache = max(cache, *u.Cache)
		}
		if input.Valid {
			cache = min(cache, input.Int64)
		} else {
			cache = 0
		}
		_, err := tx.ExecContext(ctx, "UPDATE requests SET input=?,output=?,cache=?,cache_reported=MAX(cache_reported,?),reserved=CASE WHEN ? THEN 0 ELSE reserved END,state='reported' WHERE id=?", input, output, cache, u.Cache != nil, input.Valid && output.Valid, id)
		return err
	})
}
func (l *Ledger) Rejected(ctx context.Context, id string) error {
	_, err := l.db.ExecContext(ctx, "UPDATE requests SET reserved=0,state='rejected' WHERE id=?", id)
	return err
}
func (l *Ledger) MarkRoutingTurn(ctx context.Context, turn string) error {
	_, err := l.db.ExecContext(ctx, "INSERT OR IGNORE INTO routing_turns VALUES (?)", turn)
	return err
}

type WaitRequest struct {
	Scope    string
	Turn     string
	Provider string
	AtMS     int64
	Required int64
}
type Waiting struct {
	Scope    string `json:"scope"`
	Turn     string `json:"turn"`
	Provider string `json:"provider"`
	Since    int64  `json:"since"`
	ResetAt  int64  `json:"reset_at"`
	Required int64  `json:"required"`
	Released bool   `json:"released"`
}

func (l *Ledger) Wait(ctx context.Context, r WaitRequest) error {
	if r.Scope == "" || r.Turn == "" || r.AtMS < 0 || r.AtMS > MaxSafeInteger || r.Required < 1 || r.Required > MaxSafeInteger {
		return errors.New("invalid allowance wait")
	}
	return storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		reset := r.AtMS
		providers := []string{""}
		if r.Provider != "" {
			providers = append(providers, r.Provider)
		}
		for _, p := range providers {
			q, err := quotaIn(ctx, tx, p, r.AtMS)
			if err != nil {
				return err
			}
			if q.Active && q.Remaining < r.Required {
				reset = max(reset, q.ResetAt)
			}
		}
		var lastSince int64
		sequenceErr := tx.QueryRowContext(ctx, "SELECT since FROM wait_sequences WHERE scope=?", r.Scope).Scan(&lastSince)
		if sequenceErr != nil && !errors.Is(sequenceErr, sql.ErrNoRows) {
			return sequenceErr
		}
		since := r.AtMS
		var oldTurn string
		var oldSince int64
		var released bool
		err := tx.QueryRowContext(ctx, "SELECT turn_id,at,released FROM waits WHERE scope=?", r.Scope).Scan(&oldTurn, &oldSince, &released)
		if err == nil && oldTurn == r.Turn && !released {
			since = oldSince
		} else {
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if max(lastSince, oldSince) >= MaxSafeInteger {
				return errors.New("allowance wait token exhausted")
			}
			since = max(since, lastSince+1, oldSince+1)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO wait_sequences VALUES(?,?) ON CONFLICT(scope) DO UPDATE SET since=MAX(since,excluded.since)", r.Scope, since); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO waits(scope,turn_id,at,reset_at,required,provider) VALUES (?,?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET turn_id=excluded.turn_id,at=excluded.at,reset_at=excluded.reset_at,required=excluded.required,released=0,provider=excluded.provider", r.Scope, r.Turn, since, reset, r.Required, r.Provider)
		return err
	})
}
func (l *Ledger) Waiting(ctx context.Context, scope string) (*Waiting, error) {
	return waitingIn(ctx, l.db, scope)
}
func waitingIn(ctx context.Context, db queryer, scope string) (*Waiting, error) {
	var w Waiting
	err := db.QueryRowContext(ctx, "SELECT scope,turn_id,provider,at,reset_at,required,released FROM waits WHERE scope=?", scope).Scan(&w.Scope, &w.Turn, &w.Provider, &w.Since, &w.ResetAt, &w.Required, &w.Released)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &w, err
}
func (l *Ledger) Ready(ctx context.Context, scope string, atMS int64) (bool, error) {
	w, err := l.Waiting(ctx, scope)
	if err != nil || w == nil {
		return false, err
	}
	providers := []string{""}
	if w.Provider != "" {
		providers = append(providers, w.Provider)
	}
	for _, p := range providers {
		q, err := quotaIn(ctx, l.db, p, atMS)
		if err != nil {
			return false, err
		}
		if q.Active && (!q.Policy.AutoResume && !w.Released || q.Remaining < w.Required) {
			return false, nil
		}
	}
	return true, nil
}
func (l *Ledger) ClearWait(ctx context.Context, scope string) error {
	_, err := l.db.ExecContext(ctx, "DELETE FROM waits WHERE scope=?", scope)
	return err
}

// ClearWaitForTurn cannot erase a different execution's newer wait.
func (l *Ledger) ClearWaitForTurn(ctx context.Context, scope, turn string) error {
	_, err := l.db.ExecContext(ctx, "DELETE FROM waits WHERE scope=? AND turn_id=?", scope, turn)
	return err
}
