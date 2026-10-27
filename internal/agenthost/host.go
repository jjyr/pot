package agenthost

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	webassets "github.com/jjyr/pot/web"
)

const hostVersion = "11"
const hostListen = "127.0.0.1:8792"
const hostPeerListen = "127.0.0.1:8793"

type agentHost struct {
	adapter                  Runtime
	db, tasks                *sql.DB
	secrets                  *secrets.Store
	links                    *agentlink.Service
	mailbox                  *hostMailbox
	backend                  *backend
	identity                 agentlink.Identity
	peerIdentity             peer.Identity
	peerKey                  ed25519.PrivateKey
	peerRegistry             *linkhost.Registry
	token, endpoint          string
	accessMu                 sync.Mutex
	loopbackPeer, directPeer *linkhost.PeerListener
	networkError             string
	peerCtx                  context.Context
	additionalPeers          map[string]*linkhost.PeerListener
	discoveryMu              sync.Mutex
	discoveryAt              time.Time
	discovered               []hostAddress
	discover                 func(context.Context) []hostAddress
	browserMu                sync.Mutex
	browserTokens            map[string]time.Time
}

func openAgentHost(root string, runtimes ...Runtime) (*agentHost, error) {
	adapter := runtimeOrDefault(runtimes)
	if !filepath.IsAbs(root) {
		return nil, errors.New("absolute_host_root_required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("host_root_must_be_owner_only")
	}
	db, err := storage.OpenDatabase(filepath.Join(root, "control.sqlite"), storage.ControlDatabase)
	if err != nil {
		return nil, err
	}
	h := &agentHost{adapter: adapter, db: db, peerCtx: context.Background()}
	ok := false
	defer func() {
		if !ok {
			h.close()
		}
	}()
	h.secrets, err = secrets.Open(filepath.Join(root, "control.sqlite"), secrets.FileKey{Path: filepath.Join(root, "master.key"), Create: true})
	if err != nil {
		return nil, err
	}
	var id, ref, savedAdapter string
	err = db.QueryRow("SELECT node_id,owner_secret_ref,endpoint,adapter FROM agent_adapter_host WHERE singleton=1").Scan(&id, &ref, &h.endpoint, &savedAdapter)
	if errors.Is(err, sql.ErrNoRows) {
		id = adapter.Kind + "-" + identifier()
		savedAdapter = adapter.Kind
		ref = adapter.Kind + "-host-owner-" + id
		token := identifier()
		if err = h.secrets.Put(context.Background(), ref, []byte(token)); err != nil {
			return nil, err
		}
		_, err = db.Exec("INSERT INTO agent_adapter_host(singleton,node_id,owner_secret_ref,endpoint,adapter) VALUES(1,?,?, '',?)", id, ref, savedAdapter)
	}
	if err != nil {
		return nil, err
	}
	if savedAdapter != adapter.Kind {
		return nil, errors.New("agent_runtime_root_mismatch")
	}
	token, err := h.secrets.Get(context.Background(), ref)
	if err != nil {
		return nil, err
	}
	h.token = string(token)
	clear(token)
	h.identity = agentlink.Identity{Address: protocol.AgentRef{NodeID: id, Kind: "assistant", AgentID: adapter.Kind}, Name: adapter.Name, Adapter: adapter.Kind}
	if err = h.loadPeerIdentity(context.Background()); err != nil {
		return nil, err
	}
	h.tasks, err = storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		return nil, err
	}
	h.backend, err = newBackend(h.tasks, id, root, adapter)
	if err != nil {
		return nil, err
	}
	h.mailbox = &hostMailbox{db: h.tasks, local: h.identity.Address, backend: h.backend}
	h.links, err = agentlink.New(agentlink.Options{DB: db, Secrets: h.secrets, Mailbox: h.mailbox, Backend: h.backend, HostIdentity: h.peerIdentity, HostKey: h.peerKey, Identity: func(_ context.Context, assistant string) (agentlink.Identity, error) {
		if assistant == "" {
			return agentlink.Identity{Address: protocol.AgentRef{NodeID: id, Kind: "node", AgentID: id}, Name: adapter.Name, Adapter: adapter.Kind}, nil
		}
		a, err := h.backend.resolve(context.Background(), assistant)
		if err != nil {
			return agentlink.Identity{}, err
		}
		return h.backend.identity(a), nil
	}})
	if err != nil {
		return nil, err
	}
	h.backend.approvalCommitted = h.links.PublishApproval
	h.peerRegistry = linkhost.NewRegistry(h.peerIdentity, h.peerKey)
	if err = h.peerRegistry.Register(agentlink.ServiceName, h.links.Authorize, h.links.Attach); err != nil {
		return nil, err
	}
	ok = true
	return h, nil
}
func (h *agentHost) close() {
	h.accessMu.Lock()
	if h.loopbackPeer != nil {
		h.loopbackPeer.Close()
	}
	if h.directPeer != nil {
		h.directPeer.Close()
	}
	for _, listener := range h.additionalPeers {
		listener.Close()
	}
	h.accessMu.Unlock()
	if h.links != nil {
		h.links.Close()
	}
	clear(h.peerKey)
	if h.tasks != nil {
		h.tasks.Close()
	}
	if h.secrets != nil {
		h.secrets.Close()
	}
	if h.db != nil {
		h.db.Close()
	}
}
func hostOwner(r *http.Request, token string, runtimes ...Runtime) bool {
	adapter := runtimeOrDefault(runtimes)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(host).IsLoopback() && r.Host == adapter.Listen && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(token)) == 1
}
func (h *agentHost) handler() http.Handler {
	assets := http.FileServer(http.FS(webassets.FS()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || r.Host != h.runtime().Listen {
			fail(w, 403, "local_management_only")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			assets.ServeHTTP(w, r)
			return
		}
		if !hostOwner(r, h.token, h.runtime()) && !h.browserOwner(r) {
			fail(w, 401, "authentication_required")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+h.runtime().Listen {
			fail(w, 403, "cross_origin_denied")
			return
		}
		if r.URL.Path == "/api/v1/agent-host/receiver/authorization" && r.Method == "GET" {
			result, err := h.receiverAuthorization(r.Context(), r.URL.Query().Get("connection_id"))
			if handleError(w, err) {
				return
			}
			writeJSON(w, 200, result)
			return
		}
		if r.URL.Path == "/api/v1/agent-host/receiver/grant" && r.Method == "POST" {
			var grant receiverGrant
			if !body(w, r, &grant) {
				return
			}
			if handleError(w, h.saveReceiverGrant(r.Context(), grant)) {
				return
			}
			writeJSON(w, 200, map[string]bool{"saved": true})
			return
		}
		if r.URL.Path == "/api/v1/agent-host/receiver/disconnect" && r.Method == "POST" {
			var input struct {
				ConnectionID string `json:"connection_id"`
			}
			if !body(w, r, &input) {
				return
			}
			result, err := h.db.ExecContext(r.Context(), "UPDATE mcp_receiver_connections SET state='disconnected',revision=revision+1 WHERE id=?", input.ConnectionID)
			if handleError(w, err) {
				return
			}
			n, err := result.RowsAffected()
			if handleError(w, err) {
				return
			}
			if n != 1 {
				fail(w, 404, "not_found")
				return
			}
			writeJSON(w, 200, map[string]bool{"disconnected": true})
			return
		}
		if r.URL.Path == "/api/v1/agent-host/browser" && r.Method == "POST" {
			writeJSON(w, 200, h.browserPage())
			return
		}
		if r.URL.Path == "/api/v1/agent-host/health" {
			writeJSON(w, 200, map[string]any{"version": hostVersion, "identity": h.identity})
			return
		}
		if r.URL.Path == "/api/v1/agent-host/assistants" && r.Method == "POST" {
			var in struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Workspace string `json:"workspace"`
				Mode      string `json:"mode"`
				Default   bool   `json:"default"`
				Model     string `json:"model"`
				Effort    string `json:"effort"`
			}
			if !body(w, r, &in) {
				return
			}
			err := h.backend.ownerConfigure(r.Context(), assistant{ID: in.ID, Name: in.Name, Workspace: in.Workspace, Mode: in.Mode, Default: in.Default, Model: in.Model, Effort: in.Effort})
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
			directory, err := h.backend.Directory(r.Context())
			if handleError(w, err) {
				return
			}
			writeJSON(w, 200, directory)
			return
		}
		if r.URL.Path == "/api/v1/agent-host/executor" && r.Method == "POST" {
			var in struct {
				Workspace string `json:"workspace"`
				Mode      string `json:"mode"`
				Model     string `json:"model"`
				Effort    string `json:"effort"`
			}
			if !body(w, r, &in) {
				return
			}
			if err := h.backend.ownerExecutor(r.Context(), in.Workspace, in.Mode, in.Model, in.Effort); handleError(w, err) {
				return
			}
			writeJSON(w, 200, map[string]bool{"accepted": true})
			return
		}
		if r.URL.Path == "/api/v1/agent-host/tool" && r.Method == "POST" {
			var input hostToolInput
			if !body(w, r, &input) {
				return
			}
			v, err := h.tool(r.Context(), input)
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, v)
			return
		}
		h.connectionAPI(w, r)
	})
}
func (h *agentHost) connectionAPI(w http.ResponseWriter, r *http.Request) {
	var endpoint string
	if err := h.db.QueryRow("SELECT endpoint FROM agent_adapter_host WHERE singleton=1").Scan(&endpoint); handleError(w, err) {
		return
	}
	if r.URL.Path == "/api/v1/agent-links" && r.Method == "GET" {
		items, err := h.links.List(r.Context())
		if handleError(w, err) {
			return
		}
		status := "needs_access"
		h.accessMu.Lock()
		networkError := h.networkError
		h.accessMu.Unlock()
		if endpoint != "" {
			status = "ready"
		}
		if networkError != "" {
			status = "needs_access"
		}
		view := []map[string]any{}
		for _, c := range items {
			if c.State == "revoked" {
				continue
			}
			state := c.State
			if state == "ready" {
				state = "connected"
			}
			directory := c.Directory
			if directory == nil {
				directory = &agentlink.Directory{Targets: []agentlink.Endpoint{}}
			}
			view = append(view, map[string]any{"id": c.ID, "assistants": directory.Assistants(), "default_assistant_id": directory.DefaultAssistant(), "peer": map[string]any{"id": c.Peer.Address.AgentID, "node_id": c.Peer.Address.NodeID, "address": c.Endpoint, "kind": c.Peer.Adapter, "name": c.Peer.Name}, "status": state, "revision": c.CreatedAt.UnixMilli(), "last_error": c.LastError})
		}
		directory, err := h.backend.Directory(r.Context())
		if handleError(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"items": view, "directory": directory, "self_assistants": directory.Assistants(), "addresses": h.addresses(r.Context()), "self": map[string]any{"id": h.runtime().Kind, "node_id": h.identity.Address.NodeID, "kind": h.runtime().Kind, "name": h.runtime().Name, "address": endpoint, "endpoint": endpoint, "status": status, "reason_code": networkError}, "network": map[string]any{"listen": h.runtime().PeerListen, "endpoint": endpoint, "mode": "address", "status": status, "reason_code": networkError}, "identity": h.identity, "peer_identity": h.peerIdentity})
		return
	}
	if r.Method != "POST" {
		fail(w, 404, "not_found")
		return
	}
	if r.URL.Path == "/api/v1/agent-links/invitation" {
		var input struct {
			Endpoint string `json:"endpoint"`
		}
		if !body(w, r, &input) {
			return
		}
		var err error
		endpoint, err = h.selectAddress(r.Context(), input.Endpoint)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		inv, err := h.links.Invite(r.Context(), endpoint)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"address": inv.Address, "expires_at_ms": inv.ExpiresAt.UnixMilli()})
		return
	}
	if r.URL.Path != "/api/v1/agent-links" {
		fail(w, 404, "not_found")
		return
	}
	var in struct {
		Action      string `json:"action"`
		AssistantID string `json:"assistant_id"`
		Address     string `json:"address"`
		ID          string `json:"id"`
		Endpoint    string `json:"endpoint"`
		Mode        string `json:"mode"`
		Listen      string `json:"listen"`
		RequestID   string `json:"request_id"`
		Revision    int64  `json:"revision"`
	}
	if !body(w, r, &in) {
		return
	}
	var v any = map[string]bool{"ok": true}
	var err error
	switch in.Action {
	case "preview":
		var preview agentlink.Preview
		preview, err = h.links.Preview(r.Context(), in.Address)
		if err == nil {
			v = map[string]any{"peer": map[string]any{"id": preview.Identity.Address.AgentID, "node_id": preview.Identity.Address.NodeID, "kind": preview.Identity.Adapter, "name": preview.Identity.Name}, "endpoint": preview.Endpoint, "fingerprint": preview.Fingerprint, "expires_at_ms": preview.ExpiresAt.UnixMilli()}
		}
	case "connect":
		v, err = h.links.Connect(r.Context(), in.Address)
	case "reconnect":
		err = h.links.Reconnect(r.Context(), in.ID)
	case "delete":
		err = h.links.Delete(r.Context(), in.ID)
	case "disconnect":
		var c agentlink.Connection
		c, err = h.links.Get(r.Context(), in.ID)
		if err == nil {
			if in.Revision != c.CreatedAt.UnixMilli() {
				err = agentlink.ErrConflict
			} else {
				err = h.links.Disconnect(r.Context(), in.ID)
			}
		}
	case "configure":
		if in.Mode != "" && in.Mode != "address" {
			err = agentlink.ErrInvalid
			break
		}
		if in.Listen != "" && in.Listen != h.runtime().PeerListen {
			err = errors.New("agent_link_listener_restart_required")
			break
		}
		err = h.configureEndpoint(r.Context(), in.Endpoint)
	default:
		err = agentlink.ErrInvalid
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, v)
}

func (h *agentHost) probeEndpoint(ctx context.Context, endpoint string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if _, err := peer.ValidateEndpoint(ctx, endpoint); err != nil {
		return agentlink.ErrInvalid
	}
	if err := linkhost.Probe(ctx, endpoint, h.peerIdentity, h.peerKey, h.peerIdentity); err != nil {
		if errors.Is(err, peer.ErrUnauthorized) {
			return agentlink.ErrConflict
		}
		return agentlink.ErrUnavailable
	}
	return nil
}

func agentServe(root string, runtimes ...Runtime) error {
	adapter := runtimeOrDefault(runtimes)
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(root, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("agent_host_already_running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	h, err := openAgentHost(root, adapter)
	if err != nil {
		return err
	}
	defer h.close()
	listener, err := net.Listen("tcp", adapter.Listen)
	if err != nil {
		return errors.New("agent_host_port_in_use")
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = h.startPeerNetwork(ctx); err != nil {
		return err
	}
	plugins := linkhost.New()
	if err = plugins.RegisterAgent(linkhost.Messages{Name: adapter.Kind, Service: h.links}); err != nil {
		return err
	}
	if err = h.backend.recover(ctx); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		var workers sync.WaitGroup
		workers.Add(2)
		go func() { defer workers.Done(); h.backend.run(ctx) }()
		go func() { defer workers.Done(); h.backend.runMessages(ctx) }()
		plugins.Run(ctx)
		workers.Wait()
		close(done)
	}()
	server := &http.Server{Handler: h.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(c)
	}()
	err = server.Serve(listener)
	stop()
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type hostClient struct {
	adapter Runtime
	token   string
	client  *http.Client
}

var hostDaemonCommand = func(binary, root string) *exec.Cmd { return exec.Command(binary, "agent-serve", "--root", root) }

func (c hostClient) call(ctx context.Context, path string, input any, output any) error {
	method := "GET"
	var data io.Reader
	if input != nil {
		method = "POST"
		raw, _ := json.Marshal(input)
		data = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.runtime().Listen+path, data)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var problem struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&problem) == nil && problem.Error != "" {
			return errors.New(problem.Error)
		}
		return errors.New("agent_host_request_failed")
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(output)
}
func ensureAgentHost(root string, runtimes ...Runtime) (hostClient, error) {
	adapter := runtimeOrDefault(runtimes)
	var c hostClient
	if !filepath.IsAbs(root) {
		return c, errors.New("absolute_host_root_required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return c, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return c, errors.New("host_root_must_be_owner_only")
	}
	lock, err := os.OpenFile(filepath.Join(root, "start.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return c, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return c, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	daemon, err := os.OpenFile(filepath.Join(root, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return c, err
	}
	defer daemon.Close()
	if err = syscall.Flock(int(daemon.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return c, err
		}
		c, err = existingAgentHostClient(root, adapter)
		if err != nil {
			return c, err
		}
		if c.healthy() {
			return c, nil
		}
		return c, errors.New("agent_host_port_in_use_or_version_mismatch")
	}
	defer syscall.Flock(int(daemon.Fd()), syscall.LOCK_UN)
	// Refuse a foreign listener before opening or migrating this root's data.
	if conn, e := net.DialTimeout("tcp", adapter.Listen, 300*time.Millisecond); e == nil {
		conn.Close()
		return c, errors.New("agent_host_port_in_use_or_version_mismatch")
	}
	h, err := openAgentHost(root, adapter)
	if err != nil {
		return c, err
	}
	c = agentHostClient(h.token, adapter)
	h.close()
	binary, err := os.Executable()
	if err != nil {
		return c, err
	}
	log, err := os.OpenFile(filepath.Join(root, "host.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return c, err
	}
	defer log.Close()
	cmd := hostDaemonCommand(binary, root)
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// start.lock remains held until the child reports healthy. The child must
	// acquire daemon.lock itself before opening any databases.
	if err = syscall.Flock(int(daemon.Fd()), syscall.LOCK_UN); err != nil {
		return c, err
	}
	if err = cmd.Start(); err != nil {
		return c, err
	}
	cmd.Process.Release()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if c.healthy() {
			return c, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return c, errors.New("agent_host_start_failed_check_local_log")
}
func openExternalURL(address string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", address)
	} else {
		cmd = exec.Command("xdg-open", address)
	}
	return cmd.Run()
}
