package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type linkDelegation struct {
	Info     protocol.TaskInfo     `json:"info"`
	Request  agentlink.TaskRequest `json:"request"`
	Remote   agentlink.Task        `json:"remote"`
	Archived bool                  `json:"archived"`
}

func (s *Service) CreateTask(ctx context.Context, owner string, request protocol.CreateTask) (protocol.TaskInfo, error) {
	return s.createTaskFrom(ctx, owner, protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: owner}, request)
}

func (s *Service) createTaskFrom(ctx context.Context, owner string, from protocol.AgentRef, request protocol.CreateTask) (protocol.TaskInfo, error) {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	// Resolve an accepted submission before consulting mutable directories or
	// capabilities. A retry can return its receipt after target/link removal,
	// without gaining authority to submit any replacement work.
	encodedRequest, _ := json.Marshal(request)
	var previousPayload, previousBody string
	previousErr := s.integrationDB.QueryRowContext(ctx, "SELECT payload,body FROM agent_link_delegations WHERE owner=? AND request_id=?", owner, request.RequestID).Scan(&previousPayload, &previousBody)
	if previousErr == nil {
		var previous linkDelegation
		if previousPayload != string(encodedRequest) {
			return protocol.TaskInfo{}, agentlink.ErrConflict
		}
		if err := json.Unmarshal([]byte(previousBody), &previous); err != nil {
			return protocol.TaskInfo{}, err
		}
		if previous.Request.From != from {
			return protocol.TaskInfo{}, agentlink.ErrConflict
		}
		return previous.Info, nil
	}
	if !errors.Is(previousErr, sql.ErrNoRows) {
		return protocol.TaskInfo{}, previousErr
	}
	if request.Target != nil && request.LinkID == "" && request.Target.NodeID == s.options.RuntimeID && request.Target.AgentID == owner {
		request.Target = nil
	}
	if request.LinkID == "" && request.Target == nil {
		accepted, e := s.agentLinks.service.Submit(ctx, "", agentlink.TaskRequest{Configuration: &request, RequestID: request.RequestID, From: from, AssistantID: owner, Text: request.Text})
		if e != nil {
			return protocol.TaskInfo{}, e
		}
		record, e := s.Tasks.Get(ctx, owner, accepted.ID)
		if e != nil {
			return protocol.TaskInfo{}, e
		}
		if record == nil {
			return protocol.TaskInfo{}, agentlink.ErrNotFound
		}
		return record.Info, nil

	}
	if e := request.Validate(); e != nil {
		return protocol.TaskInfo{}, e
	}
	links, e := s.agentLinks.service.List(ctx)
	if e != nil {
		return protocol.TaskInfo{}, e
	}
	var selected *agentlink.Connection
	if request.LinkID == "" && request.Target != nil && request.Target.NodeID == s.options.RuntimeID {
		identity, err := s.agentLinkIdentity(ctx, request.Target.AgentID)
		if err != nil {
			return protocol.TaskInfo{}, err
		}
		host, _ := s.Machines.TransportIdentity()
		selected = &agentlink.Connection{ID: "local", PeerHost: host, Peer: identity}
	}
	for _, c := range links {
		if c.State == "revoked" {
			continue
		}
		if request.LinkID != "" && c.ID == request.LinkID || request.LinkID == "" && request.Target != nil && c.PeerHost.NodeID == request.Target.NodeID {
			copy := c
			selected = &copy
			break
		}
	}
	if selected == nil {
		return protocol.TaskInfo{}, agentlink.ErrNotFound
	}
	if request.Target != nil && selected.PeerHost.NodeID != request.Target.NodeID {
		return protocol.TaskInfo{}, agentlink.ErrConflict
	}
	// Models and permissions belong to the receiver; never forward local secrets,
	// provider profiles or executable model settings to a connected node.
	if request.Profile != nil || request.Model != nil || request.Skills != nil || request.Workspace.Mode() == "worktree" || request.Requirements.Desktop || request.Requirements.ExactBudget || request.Requirements.LiveSteering {
		return protocol.TaskInfo{}, agentlink.ErrUnsupported
	}
	r := agentlink.TaskRequest{RequestID: request.RequestID, From: from, Text: request.Text, Workspace: request.Workspace.ProjectDir}
	if request.Target != nil {
		r.AssistantID = request.Target.AgentID
	}
	id, _ := core.HashJSON([]string{"agent-link-task", owner, request.RequestID})
	payload, _ := json.Marshal(request)
	var written bool
	if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM task_writes WHERE assistant_id=? AND request_id=?)", owner, request.RequestID).Scan(&written); err != nil {
		return protocol.TaskInfo{}, err
	}
	if written {
		return protocol.TaskInfo{}, agentlink.ErrConflict
	}
	now := time.Now().UnixMilli()
	origin := &protocol.AgentLinkOrigin{LinkID: selected.ID, NodeID: selected.PeerHost.NodeID, AssistantID: r.AssistantID, Kind: selected.Peer.Adapter, Name: selected.Peer.Name}
	if selected.ID == "local" {
		origin.Kind = "local"
	}
	d := linkDelegation{Info: protocol.TaskInfo{ID: id, AssistantID: owner, Title: strings.SplitN(request.Text, "\n", 2)[0], Phase: protocol.TaskQueued, RemoteWaiting: true, CreatedAtMS: now, UpdatedAtMS: now, AgentLink: origin, ExecutorIcon: origin.Kind}, Request: r}
	raw, _ := json.Marshal(d)
	_, e = s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_delegations(id,link_id,owner,request_id,payload,body)VALUES(?,?,?,?,?,?)", id, selected.ID, owner, request.RequestID, string(payload), string(raw))
	if e == nil {
		s.wakeTasks()
		s.Changes.Notify("tasks")
	}
	return d.Info, e
}
func (s *Service) linkDelegation(ctx context.Context, owner, id string) (linkDelegation, error) {
	var d linkDelegation
	var body string
	e := s.integrationDB.QueryRowContext(ctx, "SELECT body FROM agent_link_delegations WHERE id=? AND owner=?", id, owner).Scan(&body)
	if e != nil {
		return d, e
	}
	e = json.Unmarshal([]byte(body), &d)
	return d, e
}
func (s *Service) GetTask(ctx context.Context, owner, id string) (any, error) {
	d, e := s.linkDelegation(ctx, owner, id)
	if e == nil {
		info := s.taskPresentation(ctx, d.Info)
		return map[string]any{"info": info, "task": info, "error": d.Remote.Error, "result": d.Remote.Result, "outcome": map[string]any{"text": d.Remote.Result}, "request": map[string]any{"text": d.Request.Text}, "run_id": d.Info.RunID}, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	return s.Tasks.Get(ctx, owner, id)
}
func (s *Service) StopTask(ctx context.Context, owner, id, mode string) error {
	d, e := s.linkDelegation(ctx, owner, id)
	if errors.Is(e, sql.ErrNoRows) {
		return s.Tasks.Stop(ctx, owner, id, mode)
	}
	if e != nil {
		return e
	}
	if mode != "cancel" {
		return agentlink.ErrUnsupported
	}
	if d.Remote.ID == "" {
		_, e = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_delegations SET stop_requested=1 WHERE id=? AND owner=?", id, owner)
		s.wakeTasks()
		return e
	}
	_, e = s.cancelLinkTask(ctx, d.Info.AgentLink.LinkID, d.Request.From, d.Remote.ID)
	s.wakeTasks()
	return e
}
func (s *Service) syncLinkDelegations(ctx context.Context) {
	rows, e := s.integrationDB.QueryContext(ctx, "SELECT id,link_id,owner,body,notified,stop_requested FROM agent_link_delegations ORDER BY id")
	if e != nil {
		return
	}
	type item struct {
		id, link, owner string
		d               linkDelegation
		notified        int
		stop            int
	}
	items := []item{}
	for rows.Next() {
		var it item
		var raw string
		if rows.Scan(&it.id, &it.link, &it.owner, &raw, &it.notified, &it.stop) != nil {
			rows.Close()
			return
		}
		if json.Unmarshal([]byte(raw), &it.d) != nil {
			rows.Close()
			return
		}
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		d := it.d
		if terminalLinkTask(d.Remote.State) && it.notified != 0 {
			continue
		}
		var remote agentlink.Task
		if terminalLinkTask(d.Remote.State) {
			remote = d.Remote
			e = nil
		} else if d.Remote.ID == "" {
			remote, e = s.submitLinkTask(ctx, it.link, d.Request)
		} else {
			remote, e = s.statusLinkTask(ctx, it.link, d.Request.From, d.Remote.ID)
		}
		if e != nil {
			definitive := errors.Is(e, agentlink.ErrInvalid) || errors.Is(e, agentlink.ErrUnsupported) || errors.Is(e, agentlink.ErrUnauthorized) || errors.Is(e, agentlink.ErrConflict) || errors.Is(e, agentlink.ErrNotFound) || errors.Is(e, agentlink.ErrFenced)
			if !definitive || d.Remote.ID != "" {
				continue
			}
			remote = agentlink.Task{RequestID: d.Request.RequestID, State: "failed", Error: e.Error(), UpdatedAt: time.Now().UTC()}
		}
		if it.stop != 0 && !terminalLinkTask(remote.State) {
			if cancelled, err := s.cancelLinkTask(ctx, it.link, d.Request.From, remote.ID); err == nil {
				remote = cancelled
			}
		}
		if d.Remote.ID != "" && (d.Remote.ID != remote.ID || d.Remote.RequestID != remote.RequestID || d.Remote.Assistant.Address != remote.Assistant.Address) {
			continue
		}
		d.Remote = remote
		d.Info.RunID = linkRunID(it.id)
		d.Info.Phase = protocol.TaskPhase(remote.State)
		if remote.State == "completed" {
			d.Info.Phase = protocol.TaskComplete
		}
		if remote.State == "unknown" {
			d.Info.Phase = protocol.TaskInterrupted
		}
		d.Info.RemoteWaiting = false
		d.Info.UpdatedAtMS = remote.UpdatedAt.UnixMilli()
		if d.Info.UpdatedAtMS <= 0 {
			d.Info.UpdatedAtMS = d.Info.CreatedAtMS
		}
		if remote.Assistant.Address.Valid() {
			d.Info.AgentLink.AssistantID = remote.Assistant.Address.AgentID
			d.Info.AgentLink.Name = remote.Assistant.Name
		}
		s.linkMu.Lock()
		if current, err := s.linkDelegation(ctx, it.owner, it.id); err == nil {
			d.Info.Archived = current.Info.Archived
			d.Info.ArchiveReason = current.Info.ArchiveReason
		}
		raw, _ := json.Marshal(d)
		_, e = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_delegations SET remote_id=?,body=? WHERE id=?", remote.ID, string(raw), it.id)
		s.linkMu.Unlock()
		if e != nil {
			continue
		}

		s.Changes.Notify("tasks")
		if terminalLinkTask(remote.State) && it.notified == 0 {
			if e = s.linkTaskNotification(ctx, it.id, it.owner, d); e == nil {
				s.wakeTasks()
				_, _ = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_delegations SET notified=1 WHERE id=?", it.id)
			}
		}
	}
}
func (s *Service) linkTaskNotification(ctx context.Context, id, owner string, d linkDelegation) error {
	if d.Request.From.Kind == "user" {
		return s.linkUserTaskNotification(ctx, id, owner, d)
	}
	// Receiver-owned task outcomes enter the same durable notification inbox as
	// local outcomes. IDs and routing are fixed at submission, even if defaults change.
	run := linkRunID(id)
	result := protocol.TaskResultEvent{TaskID: id, RunID: run, Title: d.Info.Title, Phase: d.Info.Phase, Text: d.Remote.Result, Error: d.Remote.Error, Origin: protocol.TaskOrigin{Kind: "manual"}}
	envelope := protocol.Envelope{ID: id, Kind: "task_result", From: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "runtime", AgentID: "agent-link"}, To: d.Request.From, ConversationID: run, Text: d.Remote.Result, Result: &result}
	if envelope.Text == "" {
		envelope.Text = d.Info.Title
	}
	err := storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		var exists bool
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_messages WHERE id=?)", id).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return nil
		}
		if e := inbox.EnqueueTx(ctx, tx, envelope, "in", "queued"); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO task_outcomes(assistant_id,task_id,title,phase,updated_at_ms)VALUES(?,?,?,?,?)", owner, id, d.Info.Title, d.Info.Phase, d.Info.UpdatedAtMS)
		return e
	})
	if err == nil {
		s.Changes.Notify("task-outcomes")
	}
	return err
}
func (s *Service) TaskPage(ctx context.Context, owner string, q protocol.TaskQuery) (protocol.TaskPage, error) {
	// Merge before filtering/pagination so remote tasks obey the existing query UX.
	readLocal := func(offset uint64) (protocol.TaskPage, error) {
		if owner == "" {
			return s.Tasks.Catalog(ctx, protocol.TaskQuery{Scope: "all", Limit: 50, Offset: offset})
		}
		return s.Tasks.Page(ctx, owner, protocol.TaskQuery{Scope: "all", Limit: 50, Offset: offset})
	}
	local, e := readLocal(0)
	if e != nil {
		return local, e
	}
	all := append([]protocol.TaskInfo{}, local.Items...)
	for local.NextOffset != nil {
		local, e = readLocal(*local.NextOffset)
		if e != nil {
			return local, e
		}
		all = append(all, local.Items...)
	}
	rows, e := s.integrationDB.QueryContext(ctx, "SELECT body FROM agent_link_delegations WHERE (?='' OR owner=?)", owner, owner)
	if e != nil {
		return protocol.TaskPage{}, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var d linkDelegation
		if e = rows.Scan(&raw); e != nil {
			return protocol.TaskPage{}, e
		}
		if e = json.Unmarshal([]byte(raw), &d); e != nil {
			return protocol.TaskPage{}, e
		}
		all = append(all, d.Info)
	}
	page := protocol.TaskPage{Items: []protocol.TaskInfo{}, Offset: q.Offset}
	filtered := []protocol.TaskInfo{}
	presentationOwners := map[string]string{}
	for _, info := range all {
		owner := info.AssistantID
		if display, ok := presentationOwners[owner]; ok {
			info.AssistantID = display
		} else {
			info = s.taskPresentation(ctx, info)
			presentationOwners[owner] = info.AssistantID
		}
		if info.Archived {
			page.Counts.Archived++
		} else if info.Phase.Scope() == "active" {
			page.Counts.Active++
		} else {
			page.Counts.Attention++
		}
		scope := q.Scope
		if scope == "" {
			scope = "active"
		}
		if q.Phase != nil && info.Phase != *q.Phase || q.Phase == nil && scope != "all" && info.Scope() != scope {
			continue
		}
		if q.Q != "" && !strings.Contains(strings.ToLower(info.Title+" "+info.ID), strings.ToLower(q.Q)) {
			continue
		}
		if q.ExecutionTarget != "" && info.AgentLink != nil && info.AgentLink.NodeID != q.ExecutionTarget {
			continue
		}
		if info.ID == q.Selected {
			copy := info
			page.Selected = &copy
		}
		filtered = append(filtered, info)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].UpdatedAtMS > filtered[j].UpdatedAtMS })
	page.Total = uint64(len(filtered))
	limit := q.Limit
	if limit == 0 {
		limit = 10
	}
	if limit > 50 {
		return page, agentlink.ErrInvalid
	}
	if q.Offset >= page.Total {
		q.Offset = 0
		if page.Total > 0 {
			q.Offset = (page.Total - 1) / limit * limit
		}
		page.Offset = q.Offset
	}
	if q.Offset < page.Total {
		end := min(q.Offset+limit, page.Total)
		page.Items = filtered[q.Offset:end]
		if end < page.Total {
			page.NextOffset = &end
		}
	}
	return page, rows.Err()
}

// The session remains the durable owner; presentation uses the conversation's
// directory key so task cards and notifications return to the original chat.
func (s *Service) taskPresentation(ctx context.Context, info protocol.TaskInfo) protocol.TaskInfo {
	var link, assistant string
	if s.integrationDB.QueryRowContext(ctx, "SELECT link_id,assistant_id FROM agent_link_chat_sessions WHERE id=?", info.AssistantID).Scan(&link, &assistant) == nil {
		info.AssistantID = "agent-link:" + link + ":" + assistant
	}
	return info
}

func (s *Service) linkUserTaskNotification(ctx context.Context, id, owner string, d linkDelegation) error {
	var link, assistant string
	if err := s.integrationDB.QueryRowContext(ctx, "SELECT link_id,assistant_id FROM agent_link_chat_sessions WHERE id=?", owner).Scan(&link, &assistant); err != nil {
		return err
	}
	text := d.Remote.Result
	if text == "" {
		text = d.Remote.Error
	}
	if text == "" {
		text = d.Info.Title
	}
	// Delivery is idempotent across the two stores: publish the conversation row
	// first, then the outcome receipt. Recovery repeats the same stable IDs.
	if _, err := s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING", id, link, assistant, id, "assistant", text, time.Now().UnixMilli()); err != nil {
		return err
	}
	err := storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO task_outcomes(assistant_id,task_id,title,phase,updated_at_ms) SELECT ?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM task_outcomes WHERE assistant_id=? AND task_id=?)", owner, id, d.Info.Title, d.Info.Phase, d.Info.UpdatedAtMS, owner, id)
		return err
	})
	if err == nil {
		s.Changes.Notify("agent-links")
		s.Changes.Notify("assistants")
		s.Changes.Notify("task-outcomes")
	}
	return err
}

func linkRunID(id string) string {
	v := linkChatID("task", id)
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:32]
}

func (s *Service) SetTaskDispositions(ctx context.Context, owner string, decisions []protocol.TaskDisposition) ([]protocol.TaskInfo, error) {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	if len(decisions) == 0 || len(decisions) > 8 {
		return nil, agentlink.ErrInvalid
	}
	seen := map[string]bool{}
	locals := []protocol.TaskDisposition{}
	linked := map[string]linkDelegation{}
	for _, decision := range decisions {
		if seen[decision.TaskID] || !core.Identifier(decision.TaskID) || !core.Identifier(decision.RunID) || len(decision.Reason) > 2048 {
			return nil, agentlink.ErrInvalid
		}
		seen[decision.TaskID] = true
		d, e := s.linkDelegation(ctx, owner, decision.TaskID)
		if errors.Is(e, sql.ErrNoRows) {
			locals = append(locals, decision)
			continue
		}
		if e != nil {
			return nil, e
		}
		if d.Info.RunID != decision.RunID || !terminalLinkTask(d.Remote.State) {
			return nil, agentlink.ErrConflict
		}
		linked[decision.TaskID] = d
	}
	result := []protocol.TaskInfo{}
	var e error
	if len(locals) > 0 {
		result, e = s.Tasks.SetDispositions(ctx, owner, locals)
		if e != nil {
			return nil, e
		}
	}
	for _, decision := range decisions {
		d, ok := linked[decision.TaskID]
		if !ok {
			continue
		}
		d.Info.Archived = decision.Archived
		d.Info.ArchiveReason = decision.Reason
		raw, _ := json.Marshal(d)
		if _, e = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_delegations SET body=? WHERE id=? AND owner=?", string(raw), decision.TaskID, owner); e != nil {
			return nil, e
		}
		result = append(result, d.Info)
	}
	s.Changes.Notify("tasks")
	return result, nil
}
func (s *Service) LinkTask(ctx context.Context, owner, id string) bool {
	_, e := s.linkDelegation(ctx, owner, id)
	return e == nil
}

func (s *Service) submitLinkTask(ctx context.Context, link string, r agentlink.TaskRequest) (agentlink.Task, error) {
	if link == "local" {
		link = ""
	}
	return s.agentLinks.service.Submit(ctx, link, r)
}
func (s *Service) statusLinkTask(ctx context.Context, link string, from protocol.AgentRef, id string) (agentlink.Task, error) {
	if link == "local" {
		link = ""
	}
	return s.agentLinks.service.Status(ctx, link, from, id)
}
func (s *Service) cancelLinkTask(ctx context.Context, link string, from protocol.AgentRef, id string) (agentlink.Task, error) {
	if link == "local" {
		link = ""
	}
	return s.agentLinks.service.Cancel(ctx, link, from, id)
}
