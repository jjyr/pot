package executor

import (
	"context"
	"errors"
	"strings"
)

// CommandInput is a trusted caller's bounded, already-authorized argv command.
// It uses the ordinary sandbox, admission and process supervisor, without a shell
// or login initialization. Input is delivered once over a private pipe, then EOF.
type CommandInput struct {
	Argv  []string
	Input string
}

// RunCommand owns a dedicated Engine scope. Failure ends the scope and waits for
// cleanup, including cancellation between polling calls.
func (e *Engine) RunCommand(ctx context.Context, r Request, input CommandInput) (output map[string]any, resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, e.Close())
		}
	}()
	if len(input.Argv) == 0 || len(input.Argv) > 64 || input.Argv[0] == "" || len(input.Input) > 4096 {
		return nil, errors.New("invalid direct command")
	}
	size := 0
	for _, arg := range input.Argv {
		size += len(arg)
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("invalid command argument")
		}
	}
	if size > 16384 || len(r.Environment) > 0 || r.Command != nil {
		return nil, errors.New("invalid direct command authority")
	}
	r.direct = &input
	r.Operation = "run_command"
	r.Args = map[string]any{"cmd": "argv", "login": false, "max_output_chars": r.Policy.MaxOutputBytes, "yield_time_ms": r.Policy.MaxYieldMS}
	// Keep one bounded output buffer across waits; never turn a truncated prefix
	// into a successful structured response.
	stdout, stderr := "", ""
	truncated := false
	invalidUTF8 := false
	for {
		result, err := e.Execute(ctx, r, nil)
		if result != nil {
			for key, dest := range map[string]*string{"stdout": &stdout, "stderr": &stderr} {
				value, _ := result[key].(string)
				remaining := max(0, int(r.Policy.MaxOutputBytes)-len(*dest))
				keep := min(len(value), remaining)
				*dest += value[:keep]
				truncated = truncated || keep < len(value)
			}
			truncated = truncated || result["truncated"] == true
			invalidUTF8 = invalidUTF8 || result["invalid_utf8"] == true
		}
		if err != nil {
			return map[string]any{"stdout": stdout, "stderr": stderr, "truncated": truncated, "execution_mode": r.Mode}, err
		}
		if result["session_id"] == nil {
			result["stdout"] = stdout
			result["stderr"] = stderr
			result["truncated"] = truncated
			result["invalid_utf8"] = invalidUTF8
			return result, nil
		}
		r.Operation = "write_stdin"
		r.direct = nil
		r.Args = map[string]any{"session_id": result["session_id"], "yield_time_ms": r.Policy.MaxYieldMS, "max_output_chars": r.Policy.MaxOutputBytes}
		// ctx cancellation terminates and confirms cleanup through executeProcess.
		if ctx.Err() != nil { // Execute's early cancellation cannot clean an existing session.
			cleanup := e.Close()
			return nil, errors.Join(ctx.Err(), cleanup)
		}
	}
}
