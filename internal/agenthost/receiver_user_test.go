package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestMCPNativeHumanProfileDeleteAndTombstone(t *testing.T) {
	origin, s, _ := receiverGatewayPot(t)
	h, _ := testHost(t)
	defer h.close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	token, connection, e := authorizeReceiver(ctx, origin+"/mcp", h.backend.nodeID, receiverConsentBrowser(t, origin))
	if e != nil {
		t.Fatal(e)
	}
	identity, _ := agentlink.NewMCPReceiver(connection, h.backend.nodeID)
	client, e := connectReceiver(ctx, origin+"/mcp", token.AccessToken, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer client.close()
	if e = h.registerReceiverAssistants(ctx, client); e != nil {
		t.Fatal(e)
	}
	target := agentlink.AssistantTarget(h.backend.nodeID, "codex")
	owner := "agent-link:" + connection + ":codex"
	var directory agentlink.Directory
	if e = client.call(ctx, "pot_directory", struct{}{}, &directory); e != nil {
		t.Fatal(e)
	}
	var captured agentlink.MCPReceiverRegistration
	for _, endpoint := range directory.Targets {
		if endpoint.Address == target {
			captured = agentlink.MCPReceiverRegistration{Version: 1, Receiver: endpoint.Identity, Capabilities: endpoint.Capabilities, Epoch: h.receiverEpoch}
		}
	}
	if captured.Receiver.Address != target {
		t.Fatal("missing actual native assistant")
	}
	// The generic collaboration entry (including model callers) has no human proof.
	if _, e = s.CallAgent(ctx, owner, agentlink.AssistantProfile, agentlink.Call{Action: "get", Body: json.RawMessage(`{}`)}); !nativeErrorIs(e, agentlink.ErrUnauthorized) {
		t.Fatal("model acquired human authority", e)
	}
	callHuman := func(op agentlink.Operation, action string, body any) (any, error) {
		t.Helper()
		raw, _ := json.Marshal(body)
		type outcome struct {
			v any
			e error
		}
		done := make(chan outcome, 1)
		go func() {
			v, e := s.CallHumanAgent(ctx, owner, op, agentlink.Call{Action: action, Body: raw})
			done <- outcome{v, e}
		}()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case r := <-done:
				return r.v, r.e
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return nil, ctx.Err()
			case <-tick.C:
				if e := h.receiverUserStep(ctx, client, identity); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	value, e := callHuman(agentlink.AssistantProfile, "get", map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	profile := value.(agentlink.Profile)
	value, e = callHuman(agentlink.AssistantProfile, "write", map[string]any{"revision": profile.Revision, "name": "Updated"})
	if e != nil {
		t.Fatal(e)
	}
	updated := value.(agentlink.Profile)
	if updated.Name != "Updated" || updated.Revision != profile.Revision+1 {
		t.Fatal(updated)
	}
	if _, e = callHuman(agentlink.AssistantProfile, "write", map[string]any{"revision": profile.Revision, "name": "Stale"}); !nativeErrorIs(e, agentlink.ErrConflict) {
		t.Fatal("stale profile accepted", e)
	}
	// A stale registration cannot overwrite a later confirmed profile projection.
	if e = client.Register(ctx, captured); e != nil {
		t.Fatal(e)
	}
	if e = client.call(ctx, "pot_directory", struct{}{}, &directory); e != nil {
		t.Fatal(e)
	}
	if directory.Capabilities(target).Version != 1 {
		t.Fatal(directory)
	}
	for _, endpoint := range directory.Targets {
		if endpoint.Address == target && endpoint.Name != "Updated" {
			t.Fatal("stale profile resurrected", endpoint)
		}
	}
	if _, e = callHuman(agentlink.AssistantDelete, "write", map[string]any{"revision": profile.Revision}); !nativeErrorIs(e, agentlink.ErrAssistantChanged) {
		t.Fatal("stale delete accepted", e)
	}
	// Real backend admission makes the assistant busy without launching a native CLI.
	envelope := protocol.Envelope{Kind: "message", ID: "busy", From: protocol.AgentRef{NodeID: "gateway-pot", Kind: "assistant", AgentID: "origin"}, To: target, ConversationID: "d115b4cf-5bfd-4b5a-812c-1dfb56555a9e", Text: "hold"}
	if _, e = h.mailbox.Deliver(ctx, envelope); e != nil {
		t.Fatal(e)
	}
	if _, e = callHuman(agentlink.AssistantDelete, "write", map[string]any{"revision": updated.Revision}); !nativeErrorIs(e, agentlink.ErrAssistantBusy) {
		t.Fatal("busy assistant deleted", e)
	}
	if _, e = h.tasks.ExecContext(ctx, "UPDATE agent_messages SET state='processed' WHERE id='busy'"); e != nil {
		t.Fatal(e)
	}
	value, e = callHuman(agentlink.AssistantDelete, "write", map[string]any{"revision": updated.Revision})
	if e != nil || !value.(agentlink.AssistantDeletion).Deleted {
		t.Fatal(value, e)
	}
	if e = client.Register(ctx, captured); e == nil {
		t.Fatal("deleted endpoint resurrected")
	}
	if e = client.call(ctx, "pot_directory", struct{}{}, &directory); e != nil {
		t.Fatal(e)
	}
	if directory.Capabilities(target).Version == 1 {
		t.Fatal("deleted assistant remains discoverable")
	}
}

func TestMCPNativeUserJournalFencesIdentityAndUnknownEffects(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	ctx := t.Context()
	identity, _ := agentlink.NewMCPReceiver("connection", h.backend.nodeID)
	request := agentlink.MCPNativeUserRequest{Version: 1, ID: "request", Connection: "connection", Epoch: h.receiverEpoch, Target: agentlink.AssistantTarget(h.backend.nodeID, "codex"), Profile: &agentlink.ProfileRequest{From: protocol.AgentRef{NodeID: "origin", Kind: "user", AgentID: "human"}, AssistantID: "codex", Action: "get"}}
	for _, mutate := range []func(*agentlink.MCPNativeUserRequest){func(r *agentlink.MCPNativeUserRequest) { r.Connection = "other" }, func(r *agentlink.MCPNativeUserRequest) { r.Epoch = "stale" }, func(r *agentlink.MCPNativeUserRequest) { r.Target.NodeID = "foreign" }, func(r *agentlink.MCPNativeUserRequest) { p := *r.Profile; p.From.Kind = "assistant"; r.Profile = &p }, func(r *agentlink.MCPNativeUserRequest) { p := *r.Profile; p.AssistantID = "other"; r.Profile = &p }} {
		bad := request
		mutate(&bad)
		if _, e := h.executeNativeUser(ctx, identity, bad); !nativeErrorIs(e, agentlink.ErrUnauthorized) {
			t.Fatal("forged human request accepted", bad, e)
		}
	}
	raw, _ := json.Marshal(request)
	if _, e := h.tasks.Exec("INSERT INTO mcp_native_user_journal(id,connection_id,request,state) VALUES(?,?,?,'executing')", request.ID, request.Connection, string(raw)); e != nil {
		t.Fatal(e)
	}
	result, e := h.executeNativeUser(ctx, identity, request)
	if e != nil || result.Error != agentlink.ErrFenced.Error() {
		t.Fatal("unknown effect replayed", result, e)
	}
}

func nativeErrorIs(err, target error) bool {
	var api protocol.APIError
	return errors.Is(err, target) || errors.As(err, &api) && api.Code == target.Error()
}

func TestMCPHumanNativeManagementKeepsReceiverOwnedScopes(t *testing.T) {
	origin, s, _ := receiverGatewayPot(t)
	h, _ := testHost(t)
	defer h.close()
	h.backend.binary = fakeBinary
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	token, connection, e := authorizeReceiver(ctx, origin+"/mcp", h.backend.nodeID, receiverConsentBrowser(t, origin))
	if e != nil {
		t.Fatal(e)
	}
	identity, _ := agentlink.NewMCPReceiver(connection, h.backend.nodeID)
	client, e := connectReceiver(ctx, origin+"/mcp", token.AccessToken, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer client.close()
	if e = client.Register(ctx, agentlink.MCPReceiverRegistration{Version: 1, Receiver: agentlink.Identity{Address: agentlink.NodeTarget(h.backend.nodeID), Name: "Native", Adapter: "codex"}, Capabilities: Codex().ReceiverCapabilities(), Epoch: h.receiverEpoch}); e != nil {
		t.Fatal(e)
	}
	if e = h.registerReceiverAssistants(ctx, client); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ManageLinkedAssistant(ctx, connection, agentlink.AssistantManagementRequest{Action: "catalog"}); !nativeErrorIs(e, agentlink.ErrUnauthorized) {
		t.Fatal("model got operator catalogue", e)
	}
	human := func(input agentlink.AssistantManagementRequest) (agentlink.AssistantManagementResult, error) {
		t.Helper()
		type outcome struct {
			v agentlink.AssistantManagementResult
			e error
		}
		done := make(chan outcome, 1)
		go func() { v, e := s.ManageHumanLinkedAssistant(ctx, connection, input); done <- outcome{v, e} }()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case result := <-done:
				return result.v, result.e
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return agentlink.AssistantManagementResult{}, ctx.Err()
			case <-tick.C:
				if e = h.receiverUserStep(ctx, client, identity); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	catalogue, e := human(agentlink.AssistantManagementRequest{Action: "catalog"})
	if e != nil || len(catalogue.Models) == 0 || len(catalogue.Workspaces) == 0 {
		t.Fatal(catalogue, e)
	}
	name := "Human created"
	model := catalogue.Models[0].ID
	request := agentlink.AssistantManagementRequest{Action: "create", RequestID: "human-create", ID: "human-created", Name: &name, Model: &model, Workspace: catalogue.Workspaces[0].ID}
	created, e := human(request)
	if e != nil || created.Assistant == nil || created.Assistant.Address != agentlink.AssistantTarget(h.backend.nodeID, "human-created") {
		t.Fatal(created, e)
	}
	retried, e := human(request)
	if e != nil || encoded(created) != encoded(retried) {
		t.Fatal("original management receipt lost", e)
	}
	wrong := request
	wrong.ID = "outside"
	wrong.RequestID = "outside-request"
	wrong.Workspace = "/arbitrary/new/path"
	if _, e = human(wrong); !nativeErrorIs(e, agentlink.ErrWorkspaceUnavailable) {
		t.Fatal("human created unprovisioned scope", e)
	}
	var directory agentlink.Directory
	if e = client.call(ctx, "pot_directory", struct{}{}, &directory); e != nil || !directory.Capabilities(created.Assistant.Address).Supports(agentlink.AssistantDelete) {
		t.Fatal("created real identity missing", directory, e)
	}
}
