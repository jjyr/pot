package agenthost

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jjyr/pot/pkg/agentlink"
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
		fmt.Fprintln(out, "usage: pot-"+adapter.Kind+"-agent assistant --root PRIVATE_DIRECTORY --id ID --workspace DIRECTORY [--name NAME] [--model MODEL] [--effort LEVEL] [--mode read-only|workspace-write] [--default]; executor --root PRIVATE_DIRECTORY --workspace DIRECTORY [--mode read-only|workspace-write]")
		fmt.Fprintln(out, "receiver-connect --root PRIVATE_DIRECTORY --url POT_MCP_URL [--connection ID]; receiver-serve|receiver-disconnect --root PRIVATE_DIRECTORY --connection ID; local Host commands accept --listen LOOPBACK_IP:PORT")
		return nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	potURL := flags.String("url", "", "Pot MCP resource URL")
	connection := flags.String("connection", "", "receiver connection ID")
	root := flags.String("root", "", "private native agent host directory")
	listen := flags.String("listen", adapter.Listen, "local Host loopback IP and port")
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
	if args[0] == "execution-tools" {
		if err := bindExecutionToolsScope(flags, root, listen); err != nil {
			return err
		}
	}
	if !hostListenValid(*listen) {
		return errors.New("invalid_host_listen")
	}
	adapter.Listen = *listen
	if *root == "" {
		return errors.New("host_root_required")
	}
	switch args[0] {
	case "execution-tools":
		return executionTools(in, out, *root, adapter)
	case "receiver-connect":
		return receiverConnect(*root, *potURL, *connection, adapter, out)
	case "receiver-serve":
		return receiverServe(*root, *connection, adapter)
	case "receiver-disconnect":
		return receiverDisconnect(*root, *connection, adapter)
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

func hostListenValid(address string) bool {
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	number, portErr := strconv.Atoi(port)
	return err == nil && ip != nil && ip.IsLoopback() && portErr == nil && number > 0 && number <= 65535
}

// Installed support uses no fixed host defaults: only its supervised execution
// supplies the scope. Explicit CLI flags may confirm, never replace, that scope.
func bindExecutionToolsScope(flags *flag.FlagSet, root, listen *string) error {
	hostRoot, hostListen := os.Getenv("POT_AGENT_HOST_ROOT"), os.Getenv("POT_AGENT_HOST_LISTEN")
	if os.Getenv("POT_AGENT_JOB_ID") == "" || os.Getenv("POT_AGENT_RUN_ID") == "" || !filepath.IsAbs(hostRoot) || !hostListenValid(hostListen) {
		return agentlink.ErrUnauthorized
	}
	mismatch := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "root" && *root != hostRoot || f.Name == "listen" && *listen != hostListen {
			mismatch = true
		}
	})
	if mismatch {
		return agentlink.ErrUnauthorized
	}
	*root, *listen = hostRoot, hostListen
	return nil
}
