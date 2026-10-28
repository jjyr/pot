package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestCLIListenerRejectsNonLoopbackAndEphemeralPorts(t *testing.T) {
	for _, address := range []string{"0.0.0.0:17756", "[::]:17756", "192.0.2.1:17756", "localhost:17756", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:http", "127.0.0.1"} {
		root := filepath.Join(t.TempDir(), "must-not-open")
		err := Main([]string{"receiver-serve", "--root", root, "--connection", "synthetic", "--listen", address}, strings.NewReader(""), io.Discard)
		if err == nil || err.Error() != "invalid_host_listen" {
			t.Fatal("unsafe listener accepted", address, err)
		}
		if _, err = os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid listener opened a Host root", err)
		}
	}
}

func TestCLIExecutionListenerMustMatchSupervisedContext(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-open")
	t.Setenv("POT_AGENT_JOB_ID", "synthetic-task")
	t.Setenv("POT_AGENT_RUN_ID", "synthetic-run")
	t.Setenv("POT_AGENT_HOST_ROOT", root)
	t.Setenv("POT_AGENT_HOST_LISTEN", "127.0.0.1:17756")
	err := Main([]string{"execution-tools", "--root", root, "--listen", "127.0.0.1:17757"}, strings.NewReader(""), io.Discard)
	if !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatal("execution context changed its local Host", err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("mismatched execution opened a Host root", err)
	}
}

func TestCLIListenerReachesSpawnedDaemonWithoutContactingOtherHost(t *testing.T) {
	var foreignRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignRequests.Add(1) }))
	defer foreign.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	root := filepath.Join(t.TempDir(), "host")
	pidPath := filepath.Join(root, "test.pid")
	t.Setenv("POT_AGENTHOST_CLI_FIXTURE", "1")
	t.Setenv("POT_AGENTHOST_CLI_PID", pidPath)
	defer func() {
		raw, err := os.ReadFile(pidPath)
		if err != nil {
			t.Error("daemon PID not recorded", err)
			return
		}
		pid, err := strconv.Atoi(string(raw))
		if err != nil {
			t.Error(err)
			return
		}
		process, err := os.FindProcess(pid)
		if err == nil {
			_ = process.Signal(syscall.SIGTERM)
			_, _ = process.Wait()
		}
	}()
	adapter := Codex()
	adapter.Listen = foreign.Listener.Addr().String()
	err = MainRuntime(adapter, []string{"executor", "--root", root, "--listen", address, "--workspace", t.TempDir()}, strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatal("CLI daemon propagation failed", err)
	}
	adapter.Listen = address
	client, err := existingAgentHostClient(root, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer client.client.CloseIdleConnections()
	if !client.healthy() || foreignRequests.Load() != 0 {
		t.Fatal("selected listener was not isolated", foreignRequests.Load())
	}
}

func TestCLIExecutionStdioKeepsListenerRootAndRunBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var hosts []*agentHost
	for range 2 {
		h, root := testHost(t)
		defer h.close()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		h.adapter = Codex()
		h.adapter.Listen = listener.Addr().String()
		h.backend.adapter = h.adapter
		server := &http.Server{Handler: h.handler()}
		go server.Serve(listener)
		defer server.Close()
		hosts = append(hosts, h)
		from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "director"}
		task, err := h.backend.Submit(ctx, agentlink.TaskRequest{From: from, Target: agentlink.NodeTarget(h.backend.nodeID), RequestID: "listener-context", Text: "synthetic context"})
		if err != nil {
			t.Fatal(err)
		}
		if err = h.backend.update(ctx, task.ID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
			t.Fatal(err)
		}
		h.backend.active[task.ID] = func() {}
		for _, run := range []string{task.RunID, "stale-run"} {
			s := Session{JobID: task.ID, RunID: run, HostRoot: root, HostListen: h.adapter.Listen}
			config, err := codexExecutionConfig(s)
			if err != nil {
				t.Fatal(err)
			}
			local := config["mcp_servers.pot_receiver"].(map[string]any)
			args := local["args"].([]string)
			if h == hosts[0] {
				args = []string{"execution-tools"}
			}
			command := exec.CommandContext(ctx, binary, args...)
			command.Env = append(nativeEnvironment(os.Environ(), s), "POT_AGENTHOST_CLI_FIXTURE=1")
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() { input.Close(); command.Process.Kill(); command.Wait() }()
				session, err := mcp.ConnectStdio(ctx, output, input, mcp.Options{})
				if err != nil {
					t.Fatal("stdio did not reach selected Host", err)
				}
				defer session.Close()
				tools, err := session.ListTools(ctx)
				if err != nil || len(tools) != 1 || tools[0].Name != "pot_execution_context" || tools[0].Annotations == nil || !tools[0].Annotations.ReadOnlyHint {
					t.Fatal("Task gained extra tools", err)
				}
				result, err := session.CallTool(ctx, "pot_execution_context", json.RawMessage(`{}`))
				if err != nil || result == nil {
					t.Fatal(err)
				}
				if run != task.RunID {
					if !result.IsError {
						t.Fatal("stale run accepted")
					}
					return
				}
				var binding agentlink.MCPExecutionBinding
				raw, err := json.Marshal(result.StructuredContent)
				if err != nil || json.Unmarshal(raw, &binding) != nil || result.IsError || binding.Task.AgentID != task.ID || binding.Task.NodeID != h.backend.nodeID || binding.Actor != binding.Task || binding.RunID != task.RunID {
					t.Fatal("stdio context crossed Host or run boundary", err)
				}
			}()
		}
	}
	wrong := agentHostClient(hosts[0].token, hosts[1].adapter)
	defer wrong.client.CloseIdleConnections()
	if wrong.healthy() {
		t.Fatal("different private roots shared owner authority")
	}
}

func TestCLIReceiverServeAndDisconnectUseSelectedListener(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	origin, _, _ := receiverGatewayPot(t)
	h, root := testHost(t)
	adapter := Codex()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		h.close()
		t.Fatal(err)
	}
	adapter.Listen = listener.Addr().String()
	listener.Close()
	connection := ""
	func() {
		defer h.close()
		token, id, err := authorizeReceiver(ctx, origin+"/mcp", h.backend.nodeID, receiverConsentBrowser(t, origin))
		if err != nil {
			t.Fatal(err)
		}
		connection = id
		if err = h.saveReceiverGrant(ctx, receiverGrant{ConnectionID: id, URL: origin + "/mcp", NodeID: h.backend.nodeID, Token: token}); err != nil {
			t.Fatal(err)
		}
	}()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "receiver-serve", "--root", root, "--connection", connection, "--listen", adapter.Listen)
	command.Env = append(os.Environ(), "POT_AGENTHOST_CLI_FIXTURE=1")
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		if err := command.Wait(); err != nil {
			t.Error("receiver CLI did not stop cleanly", err)
		}
	}()
	client, err := existingAgentHostClient(root, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer client.client.CloseIdleConnections()
	for !client.healthy() {
		select {
		case <-ctx.Done():
			t.Fatal("receiver CLI never became healthy on selected listener")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err = Main([]string{"receiver-disconnect", "--root", root, "--connection", connection, "--listen", adapter.Listen}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal("disconnect did not reach selected receiver", err)
	}
	if !client.healthy() {
		t.Fatal("disconnect stopped the local Host")
	}
	var metadata receiverAuthorization
	if err = client.call(ctx, "/api/v1/agent-host/receiver/authorization?connection_id="+connection, nil, &metadata); err != nil || metadata.NodeID != h.backend.nodeID {
		t.Fatal("local management lost the receiver identity", err)
	}
}

func TestCLIExecutionScopeRejectsIncompleteOrConflictingAuthority(t *testing.T) {
	for _, adapter := range []Runtime{Codex(), Antigravity()} {
		t.Run(adapter.Kind, func(t *testing.T) {
			for _, scenario := range []string{"no-env", "job", "run", "root", "listen", "relative-root", "non-loopback", "zero-port", "root-override", "listen-override", "empty-root-override", "empty-listen-override"} {
				t.Run(scenario, func(t *testing.T) {
					root := filepath.Join(t.TempDir(), "must-not-open")
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
					defer server.Close()
					env := map[string]string{"job": "task", "run": "run", "root": root, "listen": server.Listener.Addr().String()}
					switch scenario {
					case "no-env":
						for key := range env {
							env[key] = ""
						}
					case "job", "run", "root", "listen":
						env[scenario] = ""
					case "relative-root":
						env["root"] = "relative"
					case "non-loopback":
						env["listen"] = "0.0.0.0:12345"
					case "zero-port":
						env["listen"] = "127.0.0.1:0"
					}
					for key, name := range map[string]string{"job": "POT_AGENT_JOB_ID", "run": "POT_AGENT_RUN_ID", "root": "POT_AGENT_HOST_ROOT", "listen": "POT_AGENT_HOST_LISTEN"} {
						t.Setenv(name, env[key])
					}
					args := []string{"execution-tools"}
					switch scenario {
					case "root-override":
						args = append(args, "--root", filepath.Join(t.TempDir(), "other"))
					case "listen-override":
						args = append(args, "--listen", "127.0.0.1:1")
					case "empty-root-override":
						args = append(args, "--root", "")
					case "empty-listen-override":
						args = append(args, "--listen", "")
					}
					err := MainRuntime(adapter, args, strings.NewReader(""), io.Discard)
					if !errors.Is(err, agentlink.ErrUnauthorized) {
						t.Fatal("unsafe execution scope accepted", err)
					}
					if _, err = os.Stat(root); !os.IsNotExist(err) {
						t.Fatal("scope validation opened root", err)
					}
					if requests.Load() != 0 {
						t.Fatal("scope validation contacted a host")
					}
				})
			}
		})
	}
}
