package agent

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

// Main exposes only the Agent Link host. Legacy pair, serve, and remote MCP/SSE
// commands have been removed; connecting a node uses the shared invitation.
func Main(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(out, "usage: pot-codex-agent host|connections --root PRIVATE_DIRECTORY; assistant --root PRIVATE_DIRECTORY --id ID --name NAME --workspace DIRECTORY [--mode read-only|workspace-write] [--default]")
		return nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "private Codex Agent host directory")
	id := flags.String("id", "", "virtual assistant ID")
	name := flags.String("name", "", "assistant display name")
	workspace := flags.String("workspace", "", "owner-selected absolute workspace")
	mode := flags.String("mode", "read-only", "local sandbox mode")
	defaultAssistant := flags.Bool("default", false, "select as node default assistant")
	if flags.Parse(args[1:]) != nil || len(flags.Args()) != 0 {
		return errors.New("invalid_flags")
	}
	if *root == "" {
		return errors.New("host_root_required")
	}
	switch args[0] {
	case "agent-serve":
		return agentServe(*root)
	case "host":
		return hostMCP(in, out, *root)
	case "connections":
		client, err := ensureAgentHost(*root)
		if err != nil {
			return err
		}
		defer client.client.CloseIdleConnections()
		return openExternalURL("http://" + hostListen + "/codex-connections.html#" + client.token)
	case "assistant":
		client, err := ensureAgentHost(*root)
		if err != nil {
			return err
		}
		defer client.client.CloseIdleConnections()
		var result any
		err = client.call(context.Background(), "/api/v1/agent-host/assistants", map[string]any{"id": *id, "name": *name, "workspace": *workspace, "mode": *mode, "default": *defaultAssistant}, &result)
		if err == nil {
			fmt.Fprintln(out, encoded(result))
		}
		return err
	default:
		return errors.New("unknown_command")
	}
}
