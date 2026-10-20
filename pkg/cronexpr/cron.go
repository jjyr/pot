// Package cronexpr defines the shared five-field calendar syntax and IANA zones.
package cronexpr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func Parse(expression string) (cron.Schedule, error) {
	if len(expression) > 256 || len(strings.Fields(expression)) != 5 || strings.ContainsAny(expression, "?@=\x00\r\n") {
		return nil, fmt.Errorf("cron: expected five fields (minute hour day-of-month month day-of-week), without seconds or macros")
	}
	schedule, err := parser.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("cron: %w", err)
	}
	return schedule, nil
}

// Location accepts a saved IANA identifier, never Go's mutable Local alias.
func Location(name string) (*time.Location, error) {
	if name == "" || name == "Local" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.ContainsAny(name, "\\\x00\r\n") {
		return nil, fmt.Errorf("timezone: provide an IANA zone such as Europe/Paris or UTC")
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("timezone: unknown IANA zone %q", name)
	}
	return location, nil
}

func zoneName(value string) (string, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), ":")
	if index := strings.Index(value, "/zoneinfo/"); index >= 0 {
		value = value[index+len("/zoneinfo/"):]
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "posix/"), "right/")
	if _, err := Location(value); err != nil {
		return "", err
	}
	return value, nil
}

// SystemTimezone snapshots the owning process's OS zone as a real IANA name.
// A configured but unresolvable TZ fails explicitly instead of silently using UTC.
func SystemTimezone() (string, error) {
	if value, set := os.LookupEnv("TZ"); set {
		if name, err := zoneName(value); err == nil {
			return name, nil
		}
		return "", fmt.Errorf("timezone: Control TZ cannot be resolved to an IANA zone; specify timezone explicitly")
	}
	if path, err := filepath.EvalSymlinks("/etc/localtime"); err == nil && strings.Contains(path, "/zoneinfo/") {
		if name, err := zoneName(path); err == nil {
			return name, nil
		}
	}
	if raw, err := os.ReadFile("/etc/timezone"); err == nil {
		if name, err := zoneName(string(raw)); err == nil {
			return name, nil
		}
	}
	if name := time.Local.String(); name != "Local" {
		if name, err := zoneName(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("timezone: cannot determine Control system IANA zone; specify timezone explicitly")
}
