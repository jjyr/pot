package agenthost

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// Main exposes local host management and owner-selected execution scopes.
func Main(args []string, in io.Reader, out io.Writer) error {
	return MainRuntime(Codex(), args, in, out)
}

func MainRuntime(adapter Runtime, args []string, in io.Reader, out io.Writer) error {
	if len(args) > 0 && args[0] == "policy" && adapter.Kind == "antigravity" {
		return agyPolicy(in, out)
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(out, "usage: pot-"+adapter.Kind+"-agent host|connections --root PRIVATE_DIRECTORY; assistant --root PRIVATE_DIRECTORY --id ID --workspace DIRECTORY [--name NAME] [--model MODEL] [--effort LEVEL] [--mode read-only|workspace-write] [--default]; executor --root PRIVATE_DIRECTORY --workspace DIRECTORY [--mode read-only|workspace-write]")
		return nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "private native agent host directory")
	id := flags.String("id", "", "virtual assistant ID")
	name := flags.String("name", "", "assistant display name")
	workspace := flags.String("workspace", "", "owner-selected absolute workspace")
	mode := flags.String("mode", "read-only", "local execution permission scope")
	model := flags.String("model", "", "native model slug")
	effort := flags.String("effort", "", "native reasoning effort")
	defaultAssistant := flags.Bool("default", false, "select as node default assistant")
	if flags.Parse(args[1:]) != nil || len(flags.Args()) != 0 {
		return errors.New("invalid_flags")
	}
	if adapter.Kind == "antigravity" && args[0] == "host" && os.Getenv("POT_AGENT_JOB_ID") != "" && os.Getenv("POT_AGENT_HOST_ROOT") != "" {
		*root = os.Getenv("POT_AGENT_HOST_ROOT")
	}
	if *root == "" {
		return errors.New("host_root_required")
	}
	switch args[0] {
	case "executor":
		client, err := ensureAgentHost(*root, adapter)
		if err != nil {
			return err
		}
		defer client.client.CloseIdleConnections()
		var result any
		return client.call(context.Background(), "/api/v1/agent-host/executor", map[string]any{"workspace": *workspace, "mode": *mode, "model": *model, "effort": *effort}, &result)
	case "agent-serve":
		return agentServe(*root, adapter)
	case "host":
		return hostMCP(in, out, *root, adapter)
	case "connections":
		client, err := ensureAgentHost(*root, adapter)
		if err != nil {
			return err
		}
		defer client.client.CloseIdleConnections()
		var page struct {
			URL string `json:"url"`
		}
		if err := client.call(context.Background(), "/api/v1/agent-host/browser", map[string]any{}, &page); err != nil {
			return err
		}
		return openExternalURL(page.URL)
	case "assistant":
		client, err := ensureAgentHost(*root, adapter)
		if err != nil {
			return err
		}
		defer client.client.CloseIdleConnections()
		var result any
		err = client.call(context.Background(), "/api/v1/agent-host/assistants", map[string]any{"id": *id, "name": *name, "workspace": *workspace, "mode": *mode, "default": *defaultAssistant, "model": *model, "effort": *effort}, &result)
		if err == nil {
			fmt.Fprintln(out, encoded(result))
		}
		return err
	default:
		return errors.New("unknown_command")
	}
}
