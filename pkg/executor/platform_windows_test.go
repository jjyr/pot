package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsPowerShellHostAndProtectedRefusal(t *testing.T) {
	e, r := fixture(t)
	e.options.Admit = func(uint64) (func(), error) { return func() {}, nil }
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "[Console]::Write('host-ready-'+[char]0x4e2d+[char]0x6587)"}
	result, err := e.Execute(context.Background(), r, nil)
	if err != nil || result["exit_code"] != 0 || result["stdout"] != "host-ready-中文" {
		t.Fatal(result, err)
	}
	r.Mode = "sandbox"
	r.Args = map[string]any{"cmd": "New-Item should-not-exist"}
	result, err = e.Execute(context.Background(), r, nil)
	var prep *PreparationError
	if !errors.As(err, &prep) || result["execution_status"] != "preparation_failed" {
		t.Fatal(result, err)
	}
	if _, err = os.Stat(filepath.Join(r.CWD, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("Protected failure ran workload", err)
	}
}
func TestWindowsHostEnvironmentAndAuthorization(t *testing.T) {
	e, r := fixture(t)
	e.options.Admit = func(uint64) (func(), error) { return func() {}, nil }
	t.Setenv("POT_WINDOWS_TEST_SECRET", "must-not-inherit")
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "if ($env:POT_WINDOWS_TEST_SECRET) {throw 'ambient authority'}; [Console]::Write($env:SystemRoot)"}
	result, err := e.Execute(context.Background(), r, nil)
	if err != nil || result["exit_code"] != 0 || result["stdout"] != os.Getenv("SystemRoot") {
		t.Fatal(result, err)
	}
	r.Args = map[string]any{"cmd": "Write-Output forbidden", "tty": true}
	_, err = e.Execute(context.Background(), r, nil)
	var stopped *NotStartedError
	if !errors.As(err, &stopped) {
		t.Fatal("host TTY started without interactive authority", err)
	}
}
func TestWindowsExecutionCancellationAndConPTY(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "pipes", true: "ConPTY"}[tty], func(t *testing.T) {
			e, r := fixture(t)
			r.Operation = "exec_command"
			r.Args = map[string]any{"cmd": "[Console]::Write('cancel-ready'); Start-Sleep -Seconds 300", "yield_time_ms": 100, "interactive": true, "tty": tty}
			result, err := e.Execute(context.Background(), r, nil)
			if err != nil {
				t.Fatal(result, err)
			}
			id, ok := result["session_id"].(string)
			if !ok {
				t.Fatal(result)
			}
			r.Operation = "write_stdin"
			r.Args = map[string]any{"session_id": id, "terminate": true, "yield_time_ms": 1000}
			deadline := time.Now().Add(10 * time.Second)
			for {
				result, err = e.Execute(context.Background(), r, nil)
				if err != nil {
					t.Fatal(result, err)
				}
				if result["session_id"] == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("cancelled execution did not settle")
				}
			}
			if result["execution_status"] != "finished" {
				t.Fatal(result)
			}
		})
	}
}
func TestWindowsConPTYCommand(t *testing.T) {
	e, r := fixture(t)
	result := call(t, e, r, "exec_command", map[string]any{"cmd": "[Console]::Write('conpty-command-ok')", "tty": true, "yield_time_ms": 1000})
	deadline := time.Now().Add(10 * time.Second)
	output, _ := result["stdout"].(string)
	for result["session_id"] != nil {
		if time.Now().After(deadline) {
			t.Fatal(result)
		}
		result = call(t, e, r, "write_stdin", map[string]any{"session_id": result["session_id"], "yield_time_ms": 1000})
		output += result["stdout"].(string)
	}
	if result["exit_code"] != 0 || !strings.Contains(output, "conpty-command-ok") {
		t.Fatal(output, result)
	}
}

func TestWindowsConPTYRejectsNulEnvironment(t *testing.T) {
	e, r := fixture(t)
	r.Operation = "exec_command"
	r.Environment = map[string]string{"SERVICE_TOKEN": "value\x00INJECTED=1"}
	r.Args = map[string]any{"cmd": "New-Item should-not-exist", "tty": true, "interactive": true}
	if _, err := e.Execute(context.Background(), r, nil); err == nil {
		t.Fatal("environment injection accepted")
	}
	if _, err := os.Stat(filepath.Join(r.CWD, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("rejected environment ran command", err)
	}
}
