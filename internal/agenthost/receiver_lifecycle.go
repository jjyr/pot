package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
)

func receiverLock(root string) (*os.File, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("absolute_host_root_required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("host_root_must_be_owner_only")
	}
	lock, err := os.OpenFile(filepath.Join(root, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("agent_host_already_running")
	}
	return lock, nil
}

type receiverAuthorization struct {
	URL      string `json:"url"`
	NodeID   string `json:"node_id"`
	ClientID string `json:"client_id"`
}
type receiverGrant struct {
	ConnectionID string        `json:"connection_id"`
	URL          string        `json:"url"`
	NodeID       string        `json:"node_id"`
	Token        receiverToken `json:"token"`
}

func (h *agentHost) receiverAuthorization(ctx context.Context, id string) (receiverAuthorization, error) {
	result := receiverAuthorization{NodeID: h.backend.nodeID}
	if id == "" {
		return result, nil
	}
	var ref string
	if err := h.db.QueryRowContext(ctx, "SELECT url,node_id,secret_ref FROM mcp_receiver_connections WHERE id=?", id).Scan(&result.URL, &result.NodeID, &ref); err != nil {
		return result, err
	}
	raw, err := h.secrets.Get(ctx, ref)
	if err != nil {
		return result, err
	}
	defer clear(raw)
	var saved receiverToken
	if err = json.Unmarshal(raw, &saved); err != nil {
		return result, err
	}
	if saved.SchemaVersion != 1 || result.NodeID != h.backend.nodeID {
		return result, agentlink.ErrUnauthorized
	}
	result.ClientID = saved.ClientID
	return result, nil
}
func (h *agentHost) saveReceiverGrant(ctx context.Context, g receiverGrant) error {
	if g.NodeID != h.backend.nodeID || g.Token.SchemaVersion != 1 || g.Token.AccessToken == "" || g.Token.TokenType != "Bearer" || !g.Token.ExpiresAt.After(time.Now()) {
		return agentlink.ErrUnauthorized
	}
	if _, err := agentlink.NewMCPReceiver(g.ConnectionID, g.NodeID); err != nil {
		return err
	}
	if _, err := receiverURL(g.URL); err != nil {
		return err
	}
	raw, err := json.Marshal(g.Token)
	if err != nil {
		return err
	}
	defer clear(raw)
	ref := "mcp-receiver-" + identifier()
	if err = h.secrets.Put(ctx, ref, raw); err != nil {
		return err
	}
	result, err := h.db.ExecContext(ctx, `INSERT INTO mcp_receiver_connections(id,url,node_id,secret_ref,state) VALUES(?,?,?,?,'active') ON CONFLICT(id) DO UPDATE SET secret_ref=excluded.secret_ref,state='active',revision=mcp_receiver_connections.revision+1 WHERE mcp_receiver_connections.url=excluded.url AND mcp_receiver_connections.node_id=excluded.node_id`, g.ConnectionID, g.URL, g.NodeID, ref)
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n != 1 {
			err = agentlink.ErrConflict
		}
	}
	if err != nil {
		_ = h.secrets.Delete(ctx, ref)
	}
	return err
}
func receiverConnect(root, endpoint, existing string, adapter Runtime, out io.Writer) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute_host_root_required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var metadata receiverAuthorization
	var save func(receiverGrant) error
	var h *agentHost
	if client, err := existingAgentHostClient(root, adapter); err == nil && client.healthy() {
		defer client.client.CloseIdleConnections()
		if err = client.call(ctx, "/api/v1/agent-host/receiver/authorization?connection_id="+url.QueryEscape(existing), nil, &metadata); err != nil {
			return err
		}
		save = func(grant receiverGrant) error {
			var result any
			return client.call(ctx, "/api/v1/agent-host/receiver/grant", grant, &result)
		}
	} else {
		lock, err := receiverLock(root)
		if err != nil {
			return err
		}
		defer lock.Close()
		h, err = openAgentHost(root, adapter)
		if err != nil {
			return err
		}
		defer h.close()
		metadata, err = h.receiverAuthorization(ctx, existing)
		if err != nil {
			return err
		}
		save = func(grant receiverGrant) error { return h.saveReceiverGrant(ctx, grant) }
	}
	if existing != "" {
		if endpoint != "" && endpoint != metadata.URL {
			return agentlink.ErrConflict
		}
		endpoint = metadata.URL
	}
	if _, err := receiverURL(endpoint); err != nil {
		return err
	}
	token, id, err := authorizeReceiver(ctx, endpoint, metadata.NodeID, openExternalURL, metadata.ClientID)
	if err != nil {
		return err
	}
	if existing != "" && id != existing {
		return agentlink.ErrFenced
	}
	if err = save(receiverGrant{id, endpoint, metadata.NodeID, token}); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		ConnectionID string `json:"connection_id"`
	}{id})
}

func receiverDisconnect(root, id string, adapter Runtime) error {
	if client, err := existingAgentHostClient(root, adapter); err == nil {
		defer client.client.CloseIdleConnections()
		if client.healthy() {
			var result any
			return client.call(context.Background(), "/api/v1/agent-host/receiver/disconnect", struct {
				ConnectionID string `json:"connection_id"`
			}{id}, &result)
		}
	}
	lock, err := receiverLock(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	h, err := openAgentHost(root, adapter)
	if err != nil {
		return err
	}
	defer h.close()
	result, err := h.db.Exec("UPDATE mcp_receiver_connections SET state='disconnected',revision=revision+1 WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return agentlink.ErrNotFound
	}
	return nil
}

func (h *agentHost) receiverLoop(ctx context.Context, id string) error {
	for ctx.Err() == nil {
		var endpoint, node, ref, state string
		if err := h.db.QueryRowContext(ctx, "SELECT url,node_id,secret_ref,state FROM mcp_receiver_connections WHERE id=?", id).Scan(&endpoint, &node, &ref, &state); err != nil {
			return err
		}
		fail := func(problem error) error {
			_, _ = h.db.ExecContext(ctx, "UPDATE mcp_receiver_connections SET state='disconnected',revision=revision+1 WHERE id=? AND secret_ref=? AND state='active'", id, ref)
			return problem
		}
		if state != "active" {
			return fail(agentlink.ErrUnauthorized)
		}
		identity, err := agentlink.NewMCPReceiver(id, node)
		if err != nil || node != h.backend.nodeID {
			return fail(agentlink.ErrUnauthorized)
		}
		raw, err := h.secrets.Get(ctx, ref)
		if err != nil {
			return fail(err)
		}
		var token receiverToken
		err = json.Unmarshal(raw, &token)
		clear(raw)
		if err != nil || token.SchemaVersion != 1 || token.TokenType != "Bearer" || token.AccessToken == "" {
			return fail(agentlink.ErrUnauthorized)
		}
		if !time.Now().Before(token.ExpiresAt) {
			return fail(errors.New("receiver_authorization_expired"))
		}
		client, err := connectReceiver(ctx, endpoint, token.AccessToken, identity)
		token.AccessToken = ""
		if err == nil {
			capabilities := h.runtime().ReceiverCapabilities()
			registration := agentlink.MCPReceiverRegistration{Version: agentlink.MCPContractVersion, Receiver: agentlink.Identity{Address: agentlink.NodeTarget(node), Name: h.runtime().Name, Adapter: h.runtime().Kind}, Capabilities: capabilities, Epoch: h.receiverEpoch}
			if err = registration.ValidateAgainst(identity, capabilities); err == nil {
				err = client.Register(ctx, registration)
			}
			if err == nil {
				err = h.registerReceiverAssistants(ctx, client)
			}
			pump := &receiverPump{epoch: h.receiverEpoch, backend: h.backend, control: h.db, identity: identity, remote: client}
			for err == nil && ctx.Err() == nil {
				err = pump.step(ctx)
				if err == nil {
					err = receiverPause(ctx, 100*time.Millisecond)
				}
			}
			client.close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if receiverPermanent(err) {
			return fail(err)
		}
		// Only reconnect transport. The saved claim request, ack/admission receipts
		// and exact event outbox are the reconciliation boundary after response loss.
		if err = receiverPause(ctx, time.Second); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// receiverServe explicitly operates the shared native Host. It does not start
// peer listeners or register a plugin transport. Losing its outbound connection
// stops admission/reporting, while execution and local owner management stay alive.
func receiverServe(root, id string, adapter Runtime) error {
	if id == "" {
		return agentlink.ErrInvalid
	}
	lock, err := receiverLock(root)
	if err != nil {
		return err
	}
	defer lock.Close()
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
	var configuredNode string
	if err = h.db.QueryRowContext(ctx, "SELECT node_id FROM mcp_receiver_connections WHERE id=?", id).Scan(&configuredNode); err != nil {
		return err
	}
	if configuredNode != h.backend.nodeID {
		return agentlink.ErrUnauthorized
	}
	if err = h.backend.recover(ctx); err != nil {
		return err
	}
	server := &http.Server{Handler: h.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	var workers sync.WaitGroup

	workers.Add(2)
	go func() { defer workers.Done(); h.backend.run(ctx) }()
	go func() {
		defer workers.Done()
		for ctx.Err() == nil {
			_ = h.receiverLoop(ctx, id)
			if ctx.Err() != nil {
				return
			}
			// A renewed grant reactivates this same connection without stopping
			// native jobs or replacing its durable receiver identity.
			for ctx.Err() == nil {
				var state string
				e := h.db.QueryRowContext(ctx, "SELECT state FROM mcp_receiver_connections WHERE id=?", id).Scan(&state)
				if e == nil && state == "active" {
					break
				}
				if receiverPause(ctx, 500*time.Millisecond) != nil {
					return
				}
			}
			if receiverPause(ctx, 500*time.Millisecond) != nil {
				return
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	err = server.Serve(listener)
	stop()
	workers.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (h *agentHost) registerReceiverAssistants(ctx context.Context, client *receiverClient) error {
	directory, err := h.backend.Directory(ctx)
	if err != nil {
		return err
	}
	for _, endpoint := range directory.Targets {
		if endpoint.Identity.Address.Kind != "assistant" || !endpoint.Capabilities.Supports(agentlink.ChatSend) {
			continue
		}
		operations := []agentlink.Operation{}
		for _, op := range []agentlink.Operation{agentlink.ChatSend, agentlink.ChatRead, agentlink.ChatReceipt} {
			if endpoint.Capabilities.Supports(op) {
				operations = append(operations, op)
			}
		}
		registration := agentlink.MCPReceiverRegistration{Version: agentlink.MCPContractVersion, Receiver: endpoint.Identity, Capabilities: agentlink.Description{Version: 1, Scope: "peer", Operations: operations}, Epoch: h.receiverEpoch}
		if err = client.Register(ctx, registration); err != nil {
			return err
		}
	}
	return nil
}
