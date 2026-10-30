package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
)

type experimentOptions struct {
	Request, GroupBy                                             string
	Model, Effort, TaskModel, TaskEffort, Provider, TaskProvider string
	Command                                                      string
	Root                                                         string
	Assistant                                                    string
	Task                                                         string
	Turn                                                         string
	From                                                         string
	To                                                           string
	Timezone                                                     string
	Prompt                                                       string
	JSON                                                         bool
	NoWait                                                       bool
	Details                                                      bool
	Offset                                                       int
}

func parseExperimentOptions(args []string, in io.Reader) (experimentOptions, error) {
	var opts experimentOptions
	if len(args) == 0 {
		return opts, fmt.Errorf("missing command")
	}
	opts.Command = args[0]
	if opts.Command != "ask" && opts.Command != "usage" {
		return opts, fmt.Errorf("unknown command %q", opts.Command)
	}
	isAsk := opts.Command == "ask"

	seen := map[string]bool{}
	var positional []string
	stopFlags := false

	setStr := func(name, val string) error {
		if val == "" {
			return fmt.Errorf("flag --%s requires non-empty value", name)
		}
		switch name {
		case "model", "task-model", "effort", "task-effort", "provider", "task-provider":
			limit := 256
			if strings.Contains(name, "effort") {
				limit = 128
			}
			if strings.TrimSpace(val) == "" || len(val) > limit {
				return fmt.Errorf("invalid --%s value", name)
			}
			if strings.Contains(name, "provider") && !core.Identifier(val) {
				return fmt.Errorf("invalid --%s identity", name)
			}
			switch name {
			case "model":
				opts.Model = val
			case "task-model":
				opts.TaskModel = val
			case "effort":
				opts.Effort = val
			case "task-effort":
				opts.TaskEffort = val
			case "provider":
				opts.Provider = val
			case "task-provider":
				opts.TaskProvider = val
			}
		case "ask":
			opts.Request = val
		case "group-by":
			opts.GroupBy = val
		case "root":
			opts.Root = val
		case "assistant":
			opts.Assistant = val
		case "task":
			opts.Task = val
		case "turn":
			opts.Turn = val
		case "from":
			opts.From = val
		case "to":
			opts.To = val
		case "timezone":
			opts.Timezone = val
		case "offset":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("invalid --offset %q", val)
			}
			if n < 0 || n > 1000000 {
				return fmt.Errorf("--offset out of range")
			}
			opts.Offset = n
		}
		seen[name] = true
		return nil
	}

	isValueFlag := func(name string) bool {
		switch name {
		case "ask", "group-by", "root", "assistant", "task", "turn", "from", "to", "timezone", "offset", "model", "effort", "task-model", "task-effort", "provider", "task-provider":
			return true
		}
		return false
	}
	isBoolFlag := func(name string) bool {
		switch name {
		case "json", "no-wait", "details":
			return true
		}
		return false
	}

	i := 1
	for i < len(args) {
		a := args[i]
		if stopFlags {
			positional = append(positional, a)
			i++
			continue
		}
		if a == "--" {
			stopFlags = true
			i++
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
			if strings.HasPrefix(name, "-") {
				return opts, fmt.Errorf("unknown flag %q", a)
			}
			val := ""
			hasEq := false
			if idx := strings.IndexByte(name, '='); idx >= 0 {
				val = name[idx+1:]
				name = name[:idx]
				hasEq = true
			}
			switch {
			case isValueFlag(name):
				if hasEq {
					if err := setStr(name, val); err != nil {
						return opts, err
					}
				} else {
					if i+1 >= len(args) {
						return opts, fmt.Errorf("flag --%s requires a value", name)
					}
					i++
					if err := setStr(name, args[i]); err != nil {
						return opts, err
					}
				}
			case isBoolFlag(name):
				if hasEq {
					return opts, fmt.Errorf("flag --%s does not take a value", name)
				}
				switch name {
				case "json":
					opts.JSON = true
				case "no-wait":
					opts.NoWait = true
				case "details":
					opts.Details = true
				}
				seen[name] = true
			default:
				return opts, fmt.Errorf("unknown flag %q", a)
			}
			i++
			continue
		}
		positional = append(positional, a)
		i++
	}

	if isAsk {
		for _, flag := range []string{"ask", "group-by", "task", "turn", "from", "to", "timezone"} {
			if seen[flag] {
				return opts, fmt.Errorf("--%s is only valid for usage", flag)
			}
		}
		if seen["details"] {
			return opts, fmt.Errorf("--details is only valid for usage")
		}
		if seen["offset"] {
			return opts, fmt.Errorf("--offset is only valid for usage")
		}
	} else {
		for _, flag := range []string{"effort", "task-model", "task-effort", "task-provider"} {
			if seen[flag] {
				return opts, fmt.Errorf("--%s is only valid for ask", flag)
			}
		}
		if seen["no-wait"] {
			return opts, fmt.Errorf("--no-wait is only valid for ask")
		}
		if seen["offset"] {
			if !seen["details"] {
				return opts, fmt.Errorf("--offset requires --details")
			}
		}
	}

	switch opts.GroupBy {
	case "", "model", "stage", "assistant", "task", "date":
	default:
		return opts, fmt.Errorf("invalid --group-by")
	}
	if opts.Request != "" && !core.Identifier(opts.Request) {
		return opts, fmt.Errorf("invalid --ask")
	}
	if opts.Assistant != "" && opts.Task != "" {
		return opts, fmt.Errorf("--assistant and --task are mutually exclusive")
	}
	if opts.Turn != "" {
		if seen["turn"] {
			if opts.Assistant == "" && opts.Task == "" {
				return opts, fmt.Errorf("--turn requires --assistant or --task")
			}
		}
	}

	if (seen["from"] && !seen["to"]) || (!seen["from"] && seen["to"]) {
		return opts, fmt.Errorf("--from and --to must be used together")
	}

	locName := opts.Timezone
	if locName == "" {
		locName = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(locName)
	if err != nil {
		return opts, fmt.Errorf("invalid timezone %q: %v", locName, err)
	}
	opts.Timezone = locName

	if isAsk && len(positional) == 0 {
		return opts, fmt.Errorf("missing prompt")
	}
	if !isAsk && len(positional) > 0 {
		return opts, fmt.Errorf("usage command does not accept prompt")
	}

	if isAsk {
		if len(positional) == 1 && positional[0] == "-" {
			b, err := io.ReadAll(io.LimitReader(in, 65537))
			if err != nil {
				return opts, fmt.Errorf("failed to read stdin: %v", err)
			}
			if len(b) > 65536 {
				return opts, fmt.Errorf("prompt exceeds 65536 bytes")
			}
			opts.Prompt = string(b)
		} else {
			opts.Prompt = strings.Join(positional, " ")
		}
		if strings.TrimSpace(opts.Prompt) == "" {
			return opts, fmt.Errorf("prompt is empty")
		}
		if len(opts.Prompt) > 65536 {
			return opts, fmt.Errorf("prompt exceeds 65536 bytes")
		}
	}

	if seen["from"] && seen["to"] {
		from, err := time.ParseInLocation("2006-01-02", opts.From, time.UTC)
		if err != nil {
			return opts, fmt.Errorf("invalid --from date %q", opts.From)
		}
		to, err := time.ParseInLocation("2006-01-02", opts.To, time.UTC)
		if err != nil {
			return opts, fmt.Errorf("invalid --to date %q", opts.To)
		}
		if to.Before(from) {
			return opts, fmt.Errorf("--to must not be before --from")
		}
		days := int(to.Sub(from).Hours() / 24)
		if days > 3659 {
			return opts, fmt.Errorf("date range exceeds 3660 days")
		}
	} else if !isAsk {
		today := time.Now().In(loc).Format("2006-01-02")
		opts.From = today
		opts.To = today
	}

	if opts.Root == "" {
		def, err := instance.DefaultPath()
		if err != nil {
			return opts, fmt.Errorf("failed to resolve default root: %v", err)
		}
		opts.Root = def
	}
	abs, err := filepath.Abs(opts.Root)
	if err != nil {
		return opts, fmt.Errorf("failed to resolve root path: %v", err)
	}
	opts.Root = abs

	for _, v := range []struct{ name, val string }{
		{"assistant", opts.Assistant},
		{"task", opts.Task},
		{"turn", opts.Turn},
	} {
		if v.val != "" {
			if !core.Identifier(v.val) {
				return opts, fmt.Errorf("invalid --%s %q", v.name, v.val)
			}
		}
	}

	return opts, nil
}
