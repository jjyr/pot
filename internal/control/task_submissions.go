package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/tasks"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (s *Service) CreateTask(ctx context.Context, owner string, request protocol.CreateTask) (protocol.TaskInfo, error) {
	return s.createTaskFrom(ctx, owner, protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: owner}, request)
}

func (s *Service) createTaskFrom(ctx context.Context, owner string, from protocol.AgentRef, request protocol.CreateTask) (protocol.TaskInfo, error) {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	self := agentlink.AssistantTarget(s.options.RuntimeID, owner)
	if request.Target != nil && *request.Target == self {
		request.Target = nil
	}
	// Exact retries resolve their original receipt before mutable routes or capabilities.
	record, previousErr := s.Tasks.FindRequest(ctx, owner, request)
	if previousErr == nil {
		if record.Execution != nil && record.Execution.Request.From != from {
			return protocol.TaskInfo{}, agentlink.ErrConflict
		}
		return record.Info, nil
	}
	if !errors.Is(previousErr, sql.ErrNoRows) {
		return protocol.TaskInfo{}, previousErr
	}
	if err := request.Validate(); err != nil {
		return protocol.TaskInfo{}, err
	}
	target := self
	if request.Target != nil {
		target = *request.Target
	}
	r := agentlink.TaskRequest{Target: target, RequestID: request.RequestID, From: from, Text: request.Text, Workspace: request.Workspace.ProjectDir}
	if target == self && from.Kind == "assistant" {
		r.Configuration = &request
		accepted, err := s.agentLinks.service.Submit(ctx, "", r)
		if err != nil {
			return protocol.TaskInfo{}, err
		}
		record, err = s.Tasks.Get(ctx, owner, accepted.ID)
		if err != nil {
			return protocol.TaskInfo{}, err
		}
		if record == nil {
			return protocol.TaskInfo{}, agentlink.ErrNotFound
		}
		return record.Info, nil
	}
	if request.Profile != nil || request.Model != nil || request.Skills != nil || request.Workspace.Mode() == "worktree" || request.Requirements != (protocol.TaskRequirements{}) || target.Kind == "node" && request.Workspace.ProjectDir != "" {
		return protocol.TaskInfo{}, agentlink.ErrUnsupported
	}
	link, err := s.agentLinkRoute(ctx, target)
	if err != nil {
		return protocol.TaskInfo{}, err
	}
	directory, err := s.agentLinks.service.Directory(ctx, link)
	if err != nil {
		return protocol.TaskInfo{}, err
	}
	if !directory.Capabilities(target).Supports(agentlink.TaskSubmit) {
		return protocol.TaskInfo{}, agentlink.ErrUnsupported
	}
	identity := agentlink.Identity{Address: agentlink.NodeTarget(s.options.RuntimeID), Adapter: "local", Name: "Pot"}
	savedLink := "local"
	if link != "" {
		connection, e := s.agentLinks.service.Get(ctx, link)
		if e != nil {
			return protocol.TaskInfo{}, e
		}
		identity = connection.Peer
		savedLink = link
	}
	id, _ := core.HashJSON([]string{"agent-link-task", owner, request.RequestID})
	now := time.Now().UnixMilli()
	origin := &protocol.AgentLinkOrigin{LinkID: savedLink, NodeID: target.NodeID, AssistantID: target.AgentID, Kind: identity.Adapter, Name: identity.Name}
	if target.Kind != "assistant" {
		origin.AssistantID = ""
	}
	if savedLink == "local" {
		origin.Kind = "local"
	}
	record = &tasks.Record{AssistantID: owner, Request: request, Info: protocol.TaskInfo{ID: id, AssistantID: owner, Title: strings.SplitN(request.Text, "\n", 2)[0], Phase: protocol.TaskQueued, RemoteWaiting: true, CreatedAtMS: now, UpdatedAtMS: now, AgentLink: origin, ExecutorIcon: origin.Kind}, Execution: &tasks.Execution{Request: r}}
	e := s.Tasks.AcceptExecution(ctx, *record)
	if e == nil {
		s.wakeTasks()
		s.Changes.Notify("tasks")
	}
	return record.Info, e
}
func (s *Service) GetTask(ctx context.Context, owner, id string) (*tasks.Record, error) {
	record, err := s.Tasks.Get(ctx, owner, id)
	if record != nil {
		record.Info = s.taskPresentation(ctx, record.Info)
	}
	return record, err
}
func (s *Service) guideTaskExecution(ctx context.Context, owner, id string, input protocol.TaskInput) (any, error) {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	d, err := s.Tasks.Execution(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	// TaskGuidance carries owner instructions only; it cannot preserve a
	// correspondence parent as reference data. Never upgrade such input to the
	// immutable owner's authority merely because the receiver supports guidance.
	if _, reference := ctx.Value(mailParentKey{}).(protocol.Envelope); reference {
		return nil, agentlink.ErrUnauthorized
	}
	if sender, bound := ctx.Value(taskInputSenderKey{}).(protocol.AgentRef); bound && sender != d.Execution.Request.From {
		return nil, agentlink.ErrUnauthorized
	}
	if !s.taskExecutionCapabilities(ctx, d).Supports(agentlink.TaskInput) || d.Execution.Receipt.ID == "" {
		return nil, agentlink.ErrUnsupported
	}
	remote, err := s.agentLinks.service.Input(ctx, taskLinkID(d.Info.AgentLink.LinkID), agentlink.TaskGuidance{From: d.Execution.Request.From, ID: d.Execution.Receipt.ID, RequestID: input.RequestID, Text: input.Text})
	if err != nil {
		return nil, err
	}
	linkRun := d.Execution.Receipt.RunID
	projectTaskReceipt(d, remote)
	_, err = s.Tasks.UpdateExecution(ctx, *d, linkRun)
	if err == nil {
		s.wakeTasks()
		s.Changes.Notify("tasks")
	}
	return remote, err
}

func (s *Service) StopTask(ctx context.Context, owner, id, mode string) error {
	d, e := s.Tasks.Execution(ctx, owner, id)
	if errors.Is(e, sql.ErrNoRows) {
		return s.Tasks.Stop(ctx, owner, id, mode)
	}
	if e != nil {
		return e
	}
	if mode != "cancel" {
		return agentlink.ErrUnsupported
	}
	if e = s.Tasks.StopExecution(ctx, owner, id); e != nil {
		return e
	}
	defer s.wakeTasks()
	if d.Execution.Receipt.ID == "" {
		return nil
	}
	_, e = s.agentLinks.service.Cancel(ctx, taskLinkID(d.Info.AgentLink.LinkID), d.Execution.Request.From, d.Execution.Receipt.ID)
	return e
}
func (s *Service) syncTaskExecutions(ctx context.Context) {
	rows, e := s.Tasks.DB().QueryContext(ctx, "SELECT id,assistant_id,body FROM tasks WHERE json_extract(body,'$.execution') IS NOT NULL ORDER BY id")
	if e != nil {
		return
	}
	type item struct {
		id, link, owner string
		d               *tasks.Record
		notified        int
		stop            int
	}
	items := []item{}
	for rows.Next() {
		var it item
		var raw string
		if rows.Scan(&it.id, &it.owner, &raw) != nil {
			rows.Close()
			return
		}
		if json.Unmarshal([]byte(raw), &it.d) != nil {
			rows.Close()
			return
		}
		it.link = it.d.Info.AgentLink.LinkID
		if it.d.Execution.Notified {
			it.notified = 1
		}
		if it.d.Execution.StopRequested {
			it.stop = 1
		}
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		d := it.d
		expectedRun := d.Execution.Receipt.RunID
		if terminalLinkTask(d.Execution.Receipt.State) && it.notified != 0 {
			continue
		}
		var remote agentlink.Task
		if terminalLinkTask(d.Execution.Receipt.State) {
			remote = d.Execution.Receipt
			e = nil
		} else if d.Execution.Receipt.ID == "" {
			remote, e = s.agentLinks.service.Submit(ctx, taskLinkID(it.link), d.Execution.Request)
		} else {
			remote, e = s.agentLinks.service.Status(ctx, taskLinkID(it.link), d.Execution.Request.From, d.Execution.Receipt.ID)
		}
		if e != nil {
			definitive := errors.Is(e, agentlink.ErrInvalid) || errors.Is(e, agentlink.ErrUnsupported) || errors.Is(e, agentlink.ErrUnauthorized) || errors.Is(e, agentlink.ErrConflict) || errors.Is(e, agentlink.ErrNotFound) || errors.Is(e, agentlink.ErrFenced)
			if !definitive || d.Execution.Receipt.ID != "" {
				_ = s.Tasks.AwaitExecution(ctx, it.owner, it.id, expectedRun)
				continue
			}
			remote = agentlink.Task{RequestID: d.Execution.Request.RequestID, State: "failed", Error: e.Error(), UpdatedAt: time.Now().UTC()}
		}
		if it.stop != 0 && !terminalLinkTask(remote.State) {
			if cancelled, err := s.agentLinks.service.Cancel(ctx, taskLinkID(it.link), d.Execution.Request.From, remote.ID); err == nil {
				remote = cancelled
			}
		}
		if d.Execution.Receipt.ID != "" && (d.Execution.Receipt.ID != remote.ID || d.Execution.Receipt.RequestID != remote.RequestID || d.Execution.Receipt.Receiver.Address != remote.Receiver.Address) {
			continue
		}
		projectTaskReceipt(d, remote)
		changed, err := s.Tasks.UpdateExecution(ctx, *d, expectedRun)
		if err != nil || !changed {
			continue
		}

		s.Changes.Notify("tasks")
		if terminalLinkTask(remote.State) && it.notified == 0 {
			if e = s.taskExecutionNotification(ctx, it.id, it.owner, d); e == nil {
				s.wakeTasks()
				_ = s.Tasks.NotifyExecution(ctx, it.owner, it.id, remote.RunID)
			}
		}
	}
}
func (s *Service) taskExecutionNotification(ctx context.Context, id, owner string, d *tasks.Record) error {
	if d.Execution.Request.From.Kind == "user" {
		return s.userTaskExecutionNotification(ctx, id, owner, d)
	}
	// Receiver-owned task outcomes enter the same durable notification inbox as
	// local outcomes. IDs and routing are fixed at submission, even if defaults change.
	run := d.Info.RunID
	if run == "" {
		run = linkRunID(id)
	}
	eventID := id
	if d.Execution.Request.Target.Kind == "node" {
		eventID, _ = core.HashJSON([]string{id, run})
	}
	result := protocol.TaskResultEvent{TaskID: id, RunID: run, Title: d.Info.Title, Phase: d.Info.Phase, Text: d.Execution.Receipt.Result, Error: d.Execution.Receipt.Error, Origin: protocol.TaskOrigin{Kind: "manual"}}
	envelope := protocol.Envelope{ID: eventID, Kind: "task_result", From: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "runtime", AgentID: "agent-link"}, To: d.Execution.Request.From, ConversationID: run, Text: d.Execution.Receipt.Result, Result: &result}
	if envelope.Text == "" {
		envelope.Text = d.Info.Title
	}
	err := storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		var exists bool
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_messages WHERE id=?)", eventID).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return nil
		}
		if e := inbox.EnqueueTx(ctx, tx, envelope, "in", "queued"); e != nil {
			return e
		}
		return nil
	})
	if err == nil {
		s.Changes.Notify("task-outcomes")
	}
	return err
}
func (s *Service) TaskPage(ctx context.Context, owner string, q protocol.TaskQuery) (protocol.TaskPage, error) {
	var page protocol.TaskPage
	var err error
	if owner == "" {
		page, err = s.Tasks.Catalog(ctx, q)
	} else {
		page, err = s.Tasks.Page(ctx, owner, q)
	}
	for i := range page.Items {
		page.Items[i] = s.taskPresentation(ctx, page.Items[i])
	}
	if page.Selected != nil {
		info := s.taskPresentation(ctx, *page.Selected)
		page.Selected = &info
	}
	return page, err
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

func (s *Service) userTaskExecutionNotification(ctx context.Context, id, owner string, d *tasks.Record) error {
	var link, assistant string
	if err := s.integrationDB.QueryRowContext(ctx, "SELECT link_id,assistant_id FROM agent_link_chat_sessions WHERE id=?", owner).Scan(&link, &assistant); err != nil {
		return err
	}
	text := d.Execution.Receipt.Result
	if text == "" {
		text = d.Execution.Receipt.Error
	}
	if text == "" {
		text = d.Info.Title
	}
	// Delivery is idempotent across the two stores: publish the conversation row
	// first, then the outcome receipt. Recovery repeats the same stable IDs.
	if _, err := s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING", id, link, assistant, id, "assistant", text, time.Now().UnixMilli()); err != nil {
		return err
	}
	s.Changes.Notify("agent-links")
	s.Changes.Notify("assistants")
	return nil
}

func linkRunID(id string) string {
	v := linkChatID("task", id)
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:32]
}

func taskLinkID(link string) string {
	if link == "local" {
		return ""
	}
	return link
}

// Caller-side controls are the intersection of the Task contract and the
// original receiver's advertised operations. Never infer controls from its type.
func (s *Service) taskExecutionCapabilities(ctx context.Context, record *tasks.Record) agentlink.Description {
	result := agentlink.Description{Version: 1, Scope: "caller", Operations: []agentlink.Operation{agentlink.TaskRead, agentlink.TaskArchive}}
	if record == nil || record.Execution == nil || record.Info.AgentLink == nil {
		return result
	}
	var directory *agentlink.Directory
	link := taskLinkID(record.Info.AgentLink.LinkID)
	if link == "" {
		if d, err := s.agentLinks.service.Directory(ctx, ""); err == nil {
			directory = &d
		}
	} else if c, err := s.agentLinks.service.Get(ctx, link); err == nil && c.State != "revoked" {
		directory = c.Directory
	}
	if directory == nil {
		return result
	}
	advertised := directory.Capabilities(record.Execution.Request.Target)
	for _, op := range []agentlink.Operation{agentlink.TaskCancel, agentlink.TaskInput} {
		if advertised.Supports(op) {
			result.Operations = append(result.Operations, op)
		}
	}
	return result
}

// Adapt receiver state into the shared catalogue once, for both polling and
// continuation receipts. Cancellation in progress remains active until confirmed.
func projectTaskReceipt(record *tasks.Record, receipt agentlink.Task) {
	record.Execution.Receipt = receipt
	record.Info.RunID = linkRunID(record.Info.ID)
	if receipt.RunID != "" {
		record.Info.RunID = linkRunID(receipt.RunID)
	}
	record.Info.Phase = protocol.TaskPhase(receipt.State)
	switch receipt.State {
	case "completed":
		record.Info.Phase = protocol.TaskComplete
	case "unknown":
		record.Info.Phase = protocol.TaskInterrupted
	case "cancelling":
		record.Info.Phase = protocol.TaskWaiting
	}
	record.Info.RemoteWaiting = false
	record.Info.UpdatedAtMS = receipt.UpdatedAt.UnixMilli()
	if record.Info.UpdatedAtMS <= 0 {
		record.Info.UpdatedAtMS = record.Info.CreatedAtMS
	}
	if receiver := receipt.Receiver; receiver.Address.Valid() {
		if receipt.Receiver.Address.Kind == "assistant" {
			record.Info.AgentLink.AssistantID = receiver.Address.AgentID
		}
		record.Info.AgentLink.Name = receiver.Name
	}
}
