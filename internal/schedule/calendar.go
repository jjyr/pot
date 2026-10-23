// Package schedule owns persistent Control schedules and their independent Task
// occurrences. It never runs an Agent, sleeps in a tool, or changes a run target.
package schedule

import (
	"fmt"
	"github.com/jjyr/pot/pkg/cronexpr"
	"github.com/jjyr/pot/pkg/protocol"
	"strings"
	"time"
	"unicode/utf8"
)

func Validate(spec protocol.ScheduleSpec) error {
	if _, err := cronexpr.Location(spec.Timezone); err != nil {
		return err
	}
	if strings.TrimSpace(spec.Title) == "" {
		return fmt.Errorf("spec.title must not be empty or whitespace-only")
	}
	if actual := utf8.RuneCountInString(spec.Title); actual > 80 {
		return fmt.Errorf("spec.title exceeds limit: actual=%d characters, limit=80 characters", actual)
	}
	if strings.TrimSpace(spec.Goal) == "" {
		return fmt.Errorf("spec.goal must not be empty or whitespace-only")
	}
	if actual := len(spec.Goal); actual > protocol.MaxScheduleGoalBytes {
		return fmt.Errorf("spec.goal exceeds limit: actual=%d UTF-8 bytes, limit=%d UTF-8 bytes (512 KiB)", actual, protocol.MaxScheduleGoalBytes)
	}
	if spec.Notify != "all" && spec.Notify != "failures" {
		return fmt.Errorf("invalid notification policy")
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if spec.Kind == "once" {
		if _, err := once(spec); err != nil {
			return err
		}
	}
	return nil
}

// localInstant enumerates actual zone intervals around the requested wall time.
// It chooses the earliest repeated-hour occurrence and rejects spring gaps,
// including half-hour transitions and date-line jumps.
func localInstant(zone *time.Location, date time.Time, hour, minute int) (time.Time, bool) {
	wall := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, time.UTC)
	start, end := wall.Add(-48*time.Hour), wall.Add(48*time.Hour)
	offsets := map[int]bool{}
	for probe := start; !probe.After(end); {
		_, offset := probe.In(zone).Zone()
		offsets[offset] = true
		_, bound := probe.In(zone).ZoneBounds()
		if bound.IsZero() || !bound.After(probe) {
			break
		}
		probe = bound
	}
	var chosen time.Time
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(zone)
		if local.Year() == date.Year() && local.Month() == date.Month() && local.Day() == date.Day() && local.Hour() == hour && local.Minute() == minute && local.Second() == 0 && (chosen.IsZero() || candidate.Before(chosen)) {
			chosen = candidate
		}
	}
	return chosen, !chosen.IsZero()
}
func once(spec protocol.ScheduleSpec) (int64, error) {
	zone, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		return 0, err
	}
	wall, err := time.Parse("2006-01-02 15:04", spec.At)
	if err != nil {
		return 0, err
	}
	instant, exists := localInstant(zone, wall, wall.Hour(), wall.Minute())
	if !exists {
		return 0, fmt.Errorf("one-off local time does not exist during daylight-saving change")
	}
	return instant.UnixMilli(), nil
}
func Next(spec protocol.ScheduleSpec, afterMS int64) (*int64, error) {
	if err := Validate(spec); err != nil {
		return nil, err
	}
	switch spec.Kind {
	case "once":
		due, err := once(spec)
		if err != nil {
			return nil, err
		}
		if due <= afterMS {
			return nil, nil
		}
		return &due, nil
	case "interval":
		step := int64(spec.EveryMinutes) * 60000
		if afterMS > int64(^uint64(0)>>1)-step {
			return nil, fmt.Errorf("schedule interval overflow")
		}
		due := afterMS + step
		return &due, nil
	case "cron":
		parsed, err := cronexpr.Parse(spec.Cron)
		if err != nil {
			return nil, err
		}
		zone, _ := cronexpr.Location(spec.Timezone)
		after := time.UnixMilli(afterMS).In(zone)
		horizon := after.AddDate(8, 0, 0)
		// robfig advances through zone transitions. Retain Pot's earlier-only
		// repeated-wall-time contract by rejecting the later duplicate.
		for attempts := 0; attempts < 3000; attempts++ {
			candidate := parsed.Next(after)
			if candidate.IsZero() {
				// The library searches through year+5. Leap days can be eight
				// years apart across a non-leap century (2096 -> 2104).
				// Continue with one bounded window, without reparsing syntax.
				start := time.Date(after.Year()+6, time.January, 1, 0, 0, 0, 0, zone)
				candidate = parsed.Next(start.Add(-time.Nanosecond))
			}
			if candidate.IsZero() || candidate.After(horizon) {
				return nil, fmt.Errorf("cron: no occurrence within eight years")
			}
			local := candidate.In(zone)
			first, exists := localInstant(zone, local, local.Hour(), local.Minute())
			if exists && first.Equal(candidate) {
				due := candidate.UnixMilli()
				return &due, nil
			}
			after = candidate
		}
		return nil, fmt.Errorf("cron: repeated-time search limit exceeded")
	default:
		return nil, fmt.Errorf("unknown schedule kind")
	}
}
func AfterDue(spec protocol.ScheduleSpec, dueMS, nowMS int64) (*int64, error) {
	if spec.Kind == "once" {
		return nil, nil
	}
	if spec.Kind == "interval" {
		step := int64(spec.EveryMinutes) * 60000
		if step <= 0 || nowMS < dueMS {
			return nil, fmt.Errorf("invalid due interval")
		}
		steps := (nowMS-dueMS)/step + 1
		if steps > (int64(^uint64(0)>>1)-dueMS)/step {
			return nil, fmt.Errorf("schedule interval overflow")
		}
		next := dueMS + steps*step
		return &next, nil
	}
	return Next(spec, nowMS)
}
