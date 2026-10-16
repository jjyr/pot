package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jjyr/pot/internal/composition"
	"github.com/jjyr/pot/internal/eval"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == composition.WorkerArgument {
		if composition.ServeWorker(context.Background(), os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out io.Writer) error {
	var result any
	var err error
	success := true
	if len(args) == 0 {
		return errors.New("usage: pot-eval run SCENARIO NEW_DIR | live SCENARIO NEW_DIR [PI_MODELS] | validate RUN_DIR | compare BASE CANDIDATE | suite|tree deterministic|live NEW_DIR")
	}
	connection := func(path string) (*eval.DeepSeekConnection, error) {
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			path = filepath.Join(home, ".pi/agent/models.json")
		}
		return eval.ReadDeepSeek(path)
	}
	switch args[0] {
	case "run", "live":
		if len(args) != 3 && (args[0] != "live" || len(args) != 4) {
			return errors.New("run/live requires a scenario and new run directory")
		}
		scenario, e := eval.ReadScenario(args[1])
		if e != nil {
			return e
		}
		var report eval.Report
		if args[0] == "run" {
			report, err = eval.Run(ctx, scenario, args[2], eval.RunOptions{})
		} else {
			path := ""
			if len(args) == 4 {
				path = args[3]
			}
			c, e := connection(path)
			if e != nil {
				return e
			}
			report, err = eval.RunLive(ctx, scenario, args[2], c, eval.DefaultLiveLimits())
		}
		result = report
		success = report.Success
	case "validate":
		if len(args) != 2 {
			return errors.New("validate requires run directory")
		}
		result, err = eval.Validate(args[1])
	case "compare":
		if len(args) != 3 {
			return errors.New("compare requires baseline and candidate directories")
		}
		a, e := eval.Validate(args[1])
		if e != nil {
			return e
		}
		b, e := eval.Validate(args[2])
		if e != nil {
			return e
		}
		result, err = eval.Compare(a, b)
	case "suite", "tree":
		if len(args) != 3 || args[1] != "deterministic" && args[1] != "live" {
			return errors.New("suite/tree requires deterministic|live and new directory")
		}
		var c *eval.DeepSeekConnection
		if args[1] == "live" {
			c, err = connection("")
			if err != nil {
				return err
			}
		}
		if args[0] == "suite" {
			var report map[string]any
			report, err = eval.RunSuite(ctx, args[2], c)
			result = report
			success = report["success"] == true
		} else {
			var report eval.TreeReport
			report, err = eval.RunTree(ctx, args[2], c)
			result = report
			success = report.Success
		}
	default:
		return errors.New("unknown Eval command")
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(result); err != nil {
		return err
	}
	if !success {
		return errors.New("evaluation assertions failed; records retained in requested directory")
	}
	return nil
}
