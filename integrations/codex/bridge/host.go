package bridge

import (
	"context"
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
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	webassets "github.com/jjyr/pot/web"
)

const hostVersion = "1"
const hostListen = "127.0.0.1:8792"
const hostPeerListen = "127.0.0.1:8793"

type agentHost struct {
	db, tasks                *sql.DB
	secrets                  *secrets.Store
	links                    *agentlink.Service
	mailbox                  *hostMailbox
	identity                 agentlink.Identity
	token, endpoint          string
	probeClient              *http.Client
	accessMu                 sync.Mutex
	loopbackPeer, directPeer *linkhost.PeerListener
	networkError             string
}

func openAgentHost(root string) (*agentHost, error) {
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
	h := &agentHost{db: db}
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
	var id, ref string
	err = db.QueryRow("SELECT control_id,owner_secret_ref,endpoint FROM codex_agent_host WHERE singleton=1").Scan(&id, &ref, &h.endpoint)
	if errors.Is(err, sql.ErrNoRows) {
		id = "codex-" + identifier()
		ref = "codex-host-owner-" + id
		token := identifier()
		if err = h.secrets.Put(context.Background(), ref, []byte(token)); err != nil {
			return nil, err
		}
		_, err = db.Exec("INSERT INTO codex_agent_host VALUES(1,?,?, '')", id, ref)
	}
	if err != nil {
		return nil, err
	}
	token, err := h.secrets.Get(context.Background(), ref)
	if err != nil {
		return nil, err
	}
	h.token = string(token)
	clear(token)
	h.identity = agentlink.Identity{Address: protocol.AgentRef{ControlID: id, Kind: "assistant", AgentID: "codex"}, Name: "Codex", Adapter: "codex"}
	h.tasks, err = storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		return nil, err
	}
	h.mailbox = &hostMailbox{db: h.tasks, local: h.identity.Address}
	h.links, err = agentlink.New(agentlink.Options{DB: db, Secrets: h.secrets, Mailbox: h.mailbox, Identity: func(_ context.Context, assistant string) (agentlink.Identity, error) {
		if assistant != "codex" {
			return agentlink.Identity{}, agentlink.ErrInvalid
		}
		return h.identity, nil
	}})
	if err != nil {
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
	h.accessMu.Unlock()
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
func hostOwner(r *http.Request, token string) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(host).IsLoopback() && r.Host == hostListen && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(token)) == 1
}
func (h *agentHost) handler() http.Handler {
	assets := http.FileServer(http.FS(webassets.FS()))
	peer := h.links.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == agentlink.Prefix+"/ready" && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]string{"control_id": h.identity.Address.ControlID})
			return
		}
		if strings.HasPrefix(r.URL.Path, agentlink.Prefix+"/") {
			peer.ServeHTTP(w, r)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || r.Host != hostListen {
			fail(w, 403, "local_management_only")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			assets.ServeHTTP(w, r)
			return
		}
		if !hostOwner(r, h.token) {
			fail(w, 401, "authentication_required")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+hostListen {
			fail(w, 403, "cross_origin_denied")
			return
		}
		if r.URL.Path == "/api/v1/agent-host/health" {
			writeJSON(w, 200, map[string]any{"version": hostVersion, "identity": h.identity})
			return
		}
		if r.URL.Path == "/api/v1/agent-host/messages" && r.Method == "GET" {
			items, err := h.mailbox.messages(r.Context())
			if handleError(w, err) {
				return
			}
			writeJSON(w, 200, map[string]any{"items": items})
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
	if err := h.db.QueryRow("SELECT endpoint FROM codex_agent_host WHERE singleton=1").Scan(&endpoint); handleError(w, err) {
		return
	}
	if r.URL.Path == "/api/v1/agent-links" && r.Method == "GET" {
		items, err := h.links.List(r.Context())
		if handleError(w, err) {
			return
		}
		var unread int
		_ = h.tasks.QueryRow("SELECT count(*) FROM agent_messages WHERE direction='incoming' AND state='queued'").Scan(&unread)
		var unconfirmed int
		_ = h.tasks.QueryRow("SELECT count(*) FROM agent_messages WHERE direction='incoming' AND state IN('read','interrupted')").Scan(&unconfirmed)
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
			view = append(view, map[string]any{"id": c.ID, "local_assistant_id": c.Local.Address.AgentID, "peer": map[string]any{"id": c.Peer.Address.AgentID, "control_id": c.Peer.Address.ControlID, "address": c.Endpoint, "kind": c.Peer.Adapter, "name": c.Peer.Name}, "status": state, "revision": c.CreatedAt.UnixMilli(), "last_error": c.LastError})
		}
		writeJSON(w, 200, map[string]any{"items": view, "self": map[string]any{"id": "codex", "control_id": h.identity.Address.ControlID, "kind": "codex", "name": "Codex", "address": endpoint, "endpoint": endpoint, "status": status, "reason_code": networkError}, "network": map[string]any{"listen": hostPeerListen, "endpoint": endpoint, "mode": "address", "status": status, "reason_code": networkError}, "identity": h.identity, "unread": unread, "unconfirmed": unconfirmed})
		return
	}
	if r.Method != "POST" {
		fail(w, 404, "not_found")
		return
	}
	if r.URL.Path == "/api/v1/agent-links/invitation" {
		inv, err := h.links.Invite(r.Context(), "codex", endpoint)
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
	case "recover_messages":
		err = h.mailbox.recoverConsumers(r.Context())
	case "connect":
		v, err = h.links.Connect(r.Context(), "codex", in.Address)
	case "disconnect":
		var c agentlink.Connection
		c, err = h.links.Get(r.Context(), in.ID)
		if err == nil {
			if in.Revision != c.CreatedAt.UnixMilli() {
				err = agentlink.ErrConflict
			} else {
				err = h.links.Revoke(r.Context(), in.ID)
			}
		}
	case "configure":
		if in.Mode != "" && in.Mode != "address" && in.Mode != "tailscale" {
			err = agentlink.ErrInvalid
			break
		}
		if in.Listen != "" && in.Listen != hostPeerListen {
			err = errors.New("agent_link_listener_restart_required")
			break
		}
		if in.Mode == "tailscale" {
			in.Endpoint, err = configureHostTailscale(r.Context())
			if err != nil {
				break
			}
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+agentlink.Prefix+"/ready", nil)
	if err != nil {
		return agentlink.ErrInvalid
	}
	client := agentlink.NewHTTPClient(h.probeClient)
	client.Timeout = 8 * time.Second
	response, err := client.Do(request)
	if err != nil {
		return agentlink.ErrUnavailable
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2049))
	var value struct {
		ControlID string `json:"control_id"`
	}
	if err != nil || len(raw) > 2048 || response.StatusCode != http.StatusOK || protocol.DecodeStrict(raw, &value) != nil || value.ControlID != h.identity.Address.ControlID {
		return agentlink.ErrConflict
	}
	return nil
}

func agentServe(root string) error {
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
	h, err := openAgentHost(root)
	if err != nil {
		return err
	}
	defer h.close()
	listener, err := net.Listen("tcp", hostListen)
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
	if err = plugins.RegisterAgent(linkhost.Messages{Name: "codex", Service: h.links}); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() { plugins.Run(ctx); close(done) }()
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
	token  string
	client *http.Client
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
	req, err := http.NewRequestWithContext(ctx, method, "http://"+hostListen+path, data)
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
		return errors.New("agent_host_request_failed")
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(output)
}
func ensureAgentHost(root string) (hostClient, error) {
	var c hostClient
	if !filepath.IsAbs(root) {
		return c, errors.New("absolute_host_root_required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return c, err
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
	h, err := openAgentHost(root)
	if err != nil {
		return c, err
	}
	c = hostClient{h.token, &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_denied") }}}
	h.close()
	healthy := func() bool {
		var v struct {
			Version string `json:"version"`
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return c.call(ctx, "/api/v1/agent-host/health", nil, &v) == nil && v.Version == hostVersion
	}
	if healthy() {
		return c, nil
	}
	if conn, e := net.DialTimeout("tcp", hostListen, 300*time.Millisecond); e == nil {
		conn.Close()
		return c, errors.New("agent_host_port_in_use_or_version_mismatch")
	}
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
	if err = cmd.Start(); err != nil {
		return c, err
	}
	cmd.Process.Release()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if healthy() {
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
