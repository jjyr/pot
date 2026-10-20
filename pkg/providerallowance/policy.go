// Package providerallowance evaluates connection-level allowance warnings without
// model calls, presentation text, or assumptions about provider account IDs.
package providerallowance

import (
	"errors"
	"math"
	"slices"
	"time"

	"github.com/jjyr/pot/pkg/core"
)

// State is an atomic producer checkpoint. Commit it with the corresponding
// inbox events; saving it before accepting the events can lose a warning.
type State struct {
	Version             int    `json:"version"`
	Active              bool   `json:"active"`
	Episode             uint64 `json:"episode"`
	RecoveryAt          string `json:"recovery_at,omitempty"`
	LastReadAt          string `json:"last_read_at,omitempty"`
	RecoveryObservation string `json:"recovery_observation,omitempty"`
}
type Options struct {
	Now              time.Time
	Threshold        float64
	MaxAge           time.Duration
	RecoveryInterval time.Duration
}
type Alert struct {
	Episode uint64                 `json:"episode"`
	Used    float64                `json:"used"`
	Account core.ProviderAllowance `json:"account"`
	Window  core.AllowanceWindow   `json:"window"`
}
type Transition struct {
	State  State
	Alerts []Alert
}

// Evaluate warns once per connection threshold episode. Windows are compared
// independently: percentages are never added, including shared-pool windows.
// Labels are evidence only; identity quality does not affect the connection's
// episode. Incomplete or stale observations cannot establish recovery.
func Evaluate(snapshot core.AllowanceSnapshot, previous State, o Options) (Transition, error) {
	if previous.Version != 0 && previous.Version != 1 {
		return Transition{}, errors.New("unsupported allowance checkpoint")
	}
	if o.Threshold == 0 {
		o.Threshold = 90
	}
	if o.MaxAge == 0 {
		o.MaxAge = 15 * time.Minute
	}
	if o.RecoveryInterval == 0 {
		o.RecoveryInterval = time.Minute
	}
	if o.Now.IsZero() || math.IsNaN(o.Threshold) || math.IsInf(o.Threshold, 0) || o.Threshold <= 0 || o.Threshold > 100 || o.MaxAge <= 0 || o.RecoveryInterval < time.Minute {
		return Transition{}, errors.New("invalid allowance policy options")
	}
	next := previous
	next.Version = 1
	result := Transition{State: next}
	if err := (core.ModelProviderResponse{Allowances: &snapshot}).Validate("read_allowances"); err != nil {
		return result, err
	}
	fresh := func(raw string) bool {
		t, err := time.Parse(time.RFC3339, raw)
		return err == nil && !t.After(o.Now.Add(30*time.Second)) && o.Now.Sub(t) <= o.MaxAge
	}
	if !fresh(snapshot.ReadAt) {
		result.State.RecoveryAt = ""
		result.State.RecoveryObservation = ""
		return result, nil
	}
	readAt, _ := time.Parse(time.RFC3339, snapshot.ReadAt)
	if previous.LastReadAt != "" {
		last, err := time.Parse(time.RFC3339, previous.LastReadAt)
		if err != nil {
			return result, errors.New("invalid allowance checkpoint time")
		}
		if !readAt.After(last) {
			return result, nil
		}
	}
	result.State.LastReadAt = snapshot.ReadAt
	complete := len(snapshot.Accounts) > 0
	constrained := 0
	observations := []string{}
	var high *Alert
	for _, a := range snapshot.Accounts {
		sourceTime := a.AsOf
		if sourceTime == "" {
			sourceTime = a.ReadAt
		}
		if sourceTime != "" {
			parsed, err := time.Parse(time.RFC3339, sourceTime)
			if err == nil {
				observations = append(observations, parsed.UTC().Format(time.RFC3339Nano))
			}
		}
		accountFresh := (a.ReadAt != "" || a.AsOf != "") && (a.ReadAt == "" || fresh(a.ReadAt)) && (a.AsOf == "" || fresh(a.AsOf))
		if !accountFresh || len(a.Windows) == 0 {
			complete = false
		}
		for _, w := range a.Windows {
			if w.Unlimited {
				continue
			}
			constrained++
			if !accountFresh || w.Used == nil {
				complete = false
				continue
			}
			if *w.Used >= o.Threshold && (high == nil || *w.Used > high.Used) {
				high = &Alert{Used: *w.Used, Account: a, Window: w}
			}
		}
	}
	if high != nil {
		result.State.RecoveryAt = ""
		result.State.RecoveryObservation = ""
		if !result.State.Active {
			if result.State.Episode == math.MaxUint64 {
				return result, errors.New("allowance episode overflow")
			}
			result.State.Episode++
			result.State.Active = true
			high.Episode = result.State.Episode
			result.Alerts = []Alert{*high}
		}
		return result, nil
	}
	if !complete || constrained == 0 {
		result.State.RecoveryAt = ""
		result.State.RecoveryObservation = ""
		return result, nil
	}
	if !result.State.Active {
		result.State.RecoveryAt = ""
		result.State.RecoveryObservation = ""
		return result, nil
	}
	slices.Sort(observations)
	observation, _ := core.HashJSON(observations)
	if result.State.RecoveryAt == "" {
		result.State.RecoveryObservation = observation
		result.State.RecoveryAt = snapshot.ReadAt
		return result, nil
	}
	start, err := time.Parse(time.RFC3339, result.State.RecoveryAt)
	if err != nil {
		return result, errors.New("invalid allowance recovery checkpoint")
	}
	if readAt.Sub(start) >= o.RecoveryInterval && result.State.RecoveryObservation != observation {
		result.State.Active = false
		result.State.RecoveryAt = ""
		result.State.RecoveryObservation = ""
	}
	return result, nil
}

// Unavailable breaks a tentative recovery after a failed read. Persist this
// checkpoint without events through the same owner-fenced acceptance path.
func Unavailable(previous State) State {
	previous.RecoveryAt = ""
	previous.RecoveryObservation = ""
	return previous
}
