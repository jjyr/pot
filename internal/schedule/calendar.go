// Package schedule owns persistent Control schedules and their independent Task
// occurrences. It never runs an Agent, sleeps in a tool, or changes a run target.
package schedule

import (
	"fmt"
	"github.com/jjyr/pot/internal/protocol"
	"strings"
	"time"
	"unicode/utf8"
)

func Validate(spec protocol.ScheduleSpec) error {
	if strings.TrimSpace(spec.Title) == "" || utf8.RuneCountInString(spec.Title) > 80 || strings.TrimSpace(spec.Goal) == "" || len(spec.Goal) > 8000 {
		return fmt.Errorf("schedule needs a 1–80 character title and goal up to 8000 bytes")
	}
	if spec.Notify != "changes" && spec.Notify != "all" && spec.Notify != "failures" {
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
	if spec.Kind == "weekly" && len(spec.Weekdays) > 7 {
		return fmt.Errorf("weekly schedule accepts at most seven weekdays")
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
	default:
		zone, _ := time.LoadLocation(spec.Timezone)
		after := time.UnixMilli(afterMS).In(zone)
		clock, _ := time.Parse("15:04", spec.Time)
		date := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, time.UTC)
		for day := 0; day <= 8; day++ {
			candidateDate := date.AddDate(0, 0, day)
			weekday := (int(candidateDate.Weekday())+6)%7 + 1
			if spec.Kind == "weekly" {
				selected := false
				for _, d := range spec.Weekdays {
					if int(d) == weekday {
						selected = true
					}
				}
				if !selected {
					continue
				}
			}
			instant, exists := localInstant(zone, candidateDate, clock.Hour(), clock.Minute())
			if exists && instant.UnixMilli() > afterMS {
				due := instant.UnixMilli()
				return &due, nil
			}
		}
		return nil, fmt.Errorf("cannot find next schedule occurrence")
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
