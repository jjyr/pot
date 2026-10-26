package systemnotifications

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

var ErrSourceChanged = errors.New("notification source authority changed")

// Owner comes only from the authenticated current directory, never registration
// input or a retained browser projection. Linked IDs retain their complete route.
type Owner struct {
	Boot         string                `json:"-"`
	Remote       string                `json:"-"`
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Space        string                `json:"space_id"`
	Control      string                `json:"control_id"`
	ReplyID      string                `json:"reply_id"`
	ReplyCount   uint64                `json:"reply_count"`
	Preview      string                `json:"preview"`
	Capabilities agentlink.Description `json:"capabilities"`
}
type sourceState struct {
	Boot, Reply string
	Count       uint64
	Cursor      *uint64
	Approvals   map[string]bool
}
type sourceDirectory struct {
	Control, Boot, Session string
	Owners                 []Owner
}

func sourceBoot(directory sourceDirectory, owner Owner) string {
	return directory.Control + "/" + directory.Boot + "/" + owner.Remote + "/" + directory.Session
}
func readJSON(ctx context.Context, source localui.Caller, path string, out any) error {
	data, err := source.Call(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func readDirectory(ctx context.Context, source localui.Caller) (sourceDirectory, error) {
	var runtime struct {
		ID   string `json:"runtime_id"`
		Boot string `json:"boot_id"`
	}
	if err := readJSON(ctx, source, "/api/v1/runtime", &runtime); err != nil {
		return sourceDirectory{}, err
	}
	if runtime.ID == "" || runtime.Boot == "" {
		return sourceDirectory{}, errors.New("notification source identity unavailable")
	}
	directory := sourceDirectory{Control: runtime.ID, Boot: runtime.Boot}
	if err := readJSON(ctx, source, "/api/v1/assistants", &directory.Owners); err != nil {
		return directory, err
	}
	for i := range directory.Owners {
		directory.Owners[i].Control = runtime.ID
		directory.Owners[i].Space = runtime.ID
		directory.Owners[i].Boot = sourceBoot(directory, directory.Owners[i])
	}
	var links struct {
		Items []struct {
			ID, Status string
			Assistants []struct {
				Address      protocol.AgentRef     `json:"address"`
				Name         string                `json:"name"`
				ReplyID      string                `json:"reply_id"`
				ReplyCount   uint64                `json:"reply_count"`
				Preview      string                `json:"preview"`
				Capabilities agentlink.Description `json:"capabilities"`
			} `json:"assistants"`
		} `json:"items"`
	}
	// A failed peer-directory read removes only that retained projection. It never
	// grants an old peer's capabilities or makes local assistants unavailable.
	if readJSON(ctx, source, "/api/v1/agent-links", &links) == nil {
		for _, link := range links.Items {
			if link.Status != "connected" {
				continue
			}
			for _, a := range link.Assistants {
				if !a.Address.Valid() || a.Address.Kind != "assistant" || !a.Capabilities.Supports(agentlink.ChatRead) {
					continue
				}
				directory.Owners = append(directory.Owners, Owner{Boot: runtime.ID + "/" + runtime.Boot + "/" + a.Address.NodeID, Remote: a.Address.NodeID, ID: localui.LinkedAssistantID(link.ID, a.Address.AgentID), Name: a.Name, Space: runtime.ID, Control: runtime.ID, ReplyID: a.ReplyID, ReplyCount: a.ReplyCount, Preview: a.Preview, Capabilities: a.Capabilities})
			}
		}
	}
	return directory, nil
}
func replyEvents(owner Owner, before sourceState, fresh bool) []Event {
	if !fresh && owner.ReplyID != "" && owner.ReplyID != before.Reply && owner.ReplyCount > before.Count {
		return []Event{event(owner, "reply", owner.ReplyID, "", "", owner.Preview)}
	}
	return nil
}

// Task and approval jobs each own their last successful baseline. A slow or
// failed job cannot block another owner or another reader of the same owner.
func taskRead(ctx context.Context, source localui.Caller, owner Owner, boot string, after *uint64, commit func(*uint64, []Event) error) (*uint64, error) {
	for {
		path := "/api/v1/tasks/outcomes?assistant_id=" + url.QueryEscape(owner.ID)
		if after != nil {
			path += fmt.Sprintf("&after=%d", *after)
		}
		var page protocol.TaskOutcomePage
		if err := readJSON(ctx, source, path, &page); err != nil {
			return after, err
		}
		if !sourceCurrent(ctx, source, owner, boot, agentlink.TaskOutcomes) {
			return after, ErrSourceChanged
		}
		if after != nil && page.Cursor < *after || page.HasMore && (after == nil || page.Cursor <= *after) {
			return after, errors.New("invalid notification Task cursor")
		}
		var events []Event
		if after != nil {
			for _, item := range page.Items {
				// Failure enters assistant recovery; only the unresolved assistant reply
				// alerts. Cancellation and intermediate phases never alert here.
				kind := ""
				if item.Phase == "complete" {
					kind = "task-complete"
				}
				if item.Phase == "interrupted" {
					kind = "task-failed"
				}
				if kind != "" {
					id, _ := json.Marshal([]any{item.ID, item.Sequence})
					events = append(events, event(owner, kind, string(id), item.ID, item.Title, ""))
				}
			}
		}
		cursor := page.Cursor
		if err := commit(&cursor, events); err != nil {
			return after, err
		}
		after = &cursor
		if !page.HasMore {
			return after, nil
		}
	}
}
func approvalRead(ctx context.Context, source localui.Caller, owner Owner, boot string, before map[string]bool, commit func(map[string]bool, []Event) error) (map[string]bool, error) {
	var page struct {
		Pending []struct {
			ID      string                       `json:"id"`
			Title   string                       `json:"task_title"`
			Request struct{ Owner, Task string } `json:"request"`
		} `json:"pending"`
	}
	if err := readJSON(ctx, source, "/api/v1/permissions?assistant_id="+url.QueryEscape(owner.ID), &page); err != nil {
		return before, err
	}
	if !sourceCurrent(ctx, source, owner, boot, agentlink.Approvals) {
		return before, ErrSourceChanged
	}
	next := map[string]bool{}
	var events []Event
	for _, a := range page.Pending {
		if a.Request.Owner != owner.ID {
			continue
		}
		next[a.ID] = true
		if before != nil && !before[a.ID] {
			events = append(events, event(owner, "approval", a.ID, a.Request.Task, a.Title, ""))
		}
	}
	if err := commit(next, events); err != nil {
		return before, err
	}
	return next, nil
}

type sourceOwner struct {
	mu                       sync.Mutex
	owner                    Owner
	boot, runtimeBoot        string
	reply                    sourceState
	taskEpoch, approvalEpoch uint64
	ctx                      context.Context
	cancel                   context.CancelFunc
	tasks, approvals         chan struct{}
}
type sourceManager struct {
	cycle     string
	h         *Hub
	source    localui.Caller
	ctx       context.Context
	mu        sync.Mutex
	owners    map[string]*sourceOwner
	directory chan struct{}
	wg        sync.WaitGroup
}

func wakeSource(queue chan struct{}) {
	select {
	case queue <- struct{}{}:
	default:
	}
}
func (m *sourceManager) notify(topics map[string]bool) {
	directory := false
	for topic := range topics {
		if strings.HasPrefix(topic, "assistant") || strings.HasPrefix(topic, "conversation") || strings.HasPrefix(topic, "runtime") || topic == "agent-links" {
			directory = true
		}
	}
	if directory {
		wakeSource(m.directory)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, owner := range m.owners {
		if topics["task-outcomes"] || topics["agent-links"] {
			wakeSource(owner.tasks)
		}
		if topics["permissions"] || topics["agent-links"] {
			wakeSource(owner.approvals)
		}
	}
}
func (m *sourceManager) refreshDirectory() error {
	directory, err := readDirectory(m.ctx, m.source)
	if err != nil {
		return err
	}
	directory.Session = m.cycle
	current := map[string]Owner{}
	for i := range directory.Owners {
		directory.Owners[i].Boot = sourceBoot(directory, directory.Owners[i])
		owner := directory.Owners[i]
		current[owner.ID] = owner
	}
	m.mu.Lock()
	for id, entry := range m.owners {
		owner, ok := current[id]
		if !ok || entry.boot != sourceBoot(directory, owner) {
			entry.cancel()
			delete(m.owners, id)
		}
	}
	m.mu.Unlock()
	m.h.mu.Lock()
	m.h.control = directory.Control
	m.h.owners = current
	m.h.mu.Unlock()
	for _, owner := range directory.Owners {
		if m.ctx.Err() != nil {
			return m.ctx.Err()
		}
		m.mu.Lock()
		entry := m.owners[owner.ID]
		m.mu.Unlock()
		fresh := entry == nil
		if fresh {
			ctx, cancel := context.WithCancel(m.ctx)
			entry = &sourceOwner{owner: owner, boot: sourceBoot(directory, owner), runtimeBoot: directory.Boot, ctx: ctx, cancel: cancel, tasks: make(chan struct{}, 1), approvals: make(chan struct{}, 1)}
		}
		entry.mu.Lock()
		events := replyEvents(owner, entry.reply, fresh)
		if len(events) > 0 && !sourceCurrent(m.ctx, m.source, owner, directory.Boot, "") {
			entry.mu.Unlock()
			if fresh {
				entry.cancel()
			}
			return ErrSourceChanged
		}
		next := sourceState{Boot: entry.boot, Reply: owner.ReplyID, Count: owner.ReplyCount}
		if err = m.h.commitSource(m.ctx, owner.ID, next, events, "reply"); err != nil {
			entry.mu.Unlock()
			if fresh {
				entry.cancel()
			}
			return err
		}
		tasksChanged := entry.owner.Capabilities.Supports(agentlink.TaskOutcomes) != owner.Capabilities.Supports(agentlink.TaskOutcomes)
		approvalsChanged := entry.owner.Capabilities.Supports(agentlink.Approvals) != owner.Capabilities.Supports(agentlink.Approvals)
		if tasksChanged {
			entry.taskEpoch++
		}
		if approvalsChanged {
			entry.approvalEpoch++
		}
		entry.owner = owner
		entry.reply = next
		entry.mu.Unlock()
		if fresh {
			m.mu.Lock()
			m.owners[owner.ID] = entry
			m.mu.Unlock()
			m.wg.Add(2)
			go m.taskLoop(entry)
			go m.approvalLoop(entry)
		}
		if fresh || tasksChanged {
			wakeSource(entry.tasks)
		}
		if fresh || approvalsChanged {
			wakeSource(entry.approvals)
		}
	}
	return nil
}

// A failed resource invalidation remains work even if no new topic arrives.
// Each independent reader owns one cancellable timer and its successful cursor.
type sourceBackoff struct {
	timer *time.Timer
	ready <-chan time.Time
	delay time.Duration
}

func (b *sourceBackoff) stop() {
	if b.timer != nil {
		b.timer.Stop()
	}
	b.ready = nil
}
func (b *sourceBackoff) finished(err error) {
	b.stop()
	if err == nil {
		b.delay = 0
		return
	}
	if b.delay == 0 {
		b.delay = 250 * time.Millisecond
	} else {
		b.delay = min(b.delay*2, 15*time.Second)
	}
	b.timer = time.NewTimer(b.delay)
	b.ready = b.timer.C
}
func (m *sourceManager) taskLoop(entry *sourceOwner) {
	defer m.wg.Done()
	retry := sourceBackoff{}
	defer retry.stop()
	var cursor *uint64
	var epoch uint64
	for {
		select {
		case <-entry.ctx.Done():
			return
		case <-entry.tasks:
		case <-retry.ready:
		}
		entry.mu.Lock()
		owner, version := entry.owner, entry.taskEpoch
		entry.mu.Unlock()
		if version != epoch {
			cursor = nil
			epoch = version
		}
		if !owner.Capabilities.Supports(agentlink.TaskOutcomes) {
			continue
		}
		var err error
		cursor, err = taskRead(entry.ctx, m.source, owner, entry.runtimeBoot, cursor, func(next *uint64, events []Event) error {
			entry.mu.Lock()
			defer entry.mu.Unlock()
			if entry.ctx.Err() != nil || entry.taskEpoch != version {
				return ErrSourceChanged
			}
			return m.h.commitSource(entry.ctx, owner.ID, sourceState{Boot: entry.boot, Cursor: next}, events, "task")
		})
		retry.finished(err)
		if errors.Is(err, ErrSourceChanged) {
			wakeSource(m.directory)
		}
	}
}
func (m *sourceManager) approvalLoop(entry *sourceOwner) {
	defer m.wg.Done()
	retry := sourceBackoff{}
	defer retry.stop()
	var previous map[string]bool
	var epoch uint64
	for {
		select {
		case <-entry.ctx.Done():
			return
		case <-entry.approvals:
		case <-retry.ready:
		}
		entry.mu.Lock()
		owner, version := entry.owner, entry.approvalEpoch
		entry.mu.Unlock()
		if version != epoch {
			previous = nil
			epoch = version
		}
		if !owner.Capabilities.Supports(agentlink.Approvals) {
			continue
		}
		var err error
		previous, err = approvalRead(entry.ctx, m.source, owner, entry.runtimeBoot, previous, func(next map[string]bool, events []Event) error {
			entry.mu.Lock()
			defer entry.mu.Unlock()
			if entry.ctx.Err() != nil || entry.approvalEpoch != version {
				return ErrSourceChanged
			}
			return m.h.commitSource(entry.ctx, owner.ID, sourceState{Boot: entry.boot, Approvals: next}, events, "approval")
		})
		retry.finished(err)
		if errors.Is(err, ErrSourceChanged) {
			wakeSource(m.directory)
		}
	}
}

// Observe owns one host stream. Its callback only invalidates queues; slow
// linked reads never hold the stream callback or another reader hostage.
func (h *Hub) Observe(ctx context.Context, source localui.Caller) {
	for ctx.Err() == nil {
		if h.resetSource(ctx) != nil {
			return
		}
		connection, cancel := context.WithCancel(ctx)
		manager := &sourceManager{cycle: rand.Text(), h: h, source: source, ctx: connection, owners: map[string]*sourceOwner{}, directory: make(chan struct{}, 1)}
		manager.wg.Add(1)
		go func() {
			defer manager.wg.Done()
			for {
				select {
				case <-connection.Done():
					return
				case <-manager.directory:
					if manager.refreshDirectory() != nil {
						cancel()
						return
					}
				}
			}
		}()
		var previous map[string]uint64
		_ = source.Stream(connection, "/api/v1/changes", func(event localui.Event) error {
			if event.Type != "changed" && event.Type != "changes" && event.Type != "message" {
				return nil
			}
			var versions map[string]uint64
			if err := json.Unmarshal(event.Data, &versions); err != nil {
				return err
			}
			dirty := map[string]bool{}
			for topic, version := range versions {
				if previous == nil || previous[topic] != version {
					dirty[topic] = true
				}
			}
			if previous == nil {
				previous = versions
				wakeSource(manager.directory)
				// Fresh owners schedule their own Task/approval baselines. Do not
				// also enqueue directory work for this same initial snapshot.
				return nil
			}
			previous = versions
			manager.notify(dirty)
			return nil
		})
		cancel()
		manager.wg.Wait()
		_ = h.resetSource(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// Resource reads may finish after owner deletion, peer revocation or a Runtime
// replacement. Revalidate current authority before committing their events or
// cursors; a retained directory can never reactivate an old capability.
func sourceCurrent(ctx context.Context, source localui.Caller, owner Owner, boot string, operation agentlink.Operation) bool {
	var runtime struct {
		ID   string `json:"runtime_id"`
		Boot string `json:"boot_id"`
	}
	if readJSON(ctx, source, "/api/v1/runtime", &runtime) != nil || runtime.ID != owner.Control || runtime.Boot != boot {
		return false
	}
	if owner.Remote == "" {
		var current []Owner
		if readJSON(ctx, source, "/api/v1/assistants", &current) != nil {
			return false
		}
		for _, item := range current {
			if item.ID == owner.ID {
				return operation == "" || item.Capabilities.Supports(operation)
			}
		}
		return false
	}
	var page struct {
		Items []struct {
			ID, Status string
			Assistants []struct {
				Address      protocol.AgentRef     `json:"address"`
				Capabilities agentlink.Description `json:"capabilities"`
			} `json:"assistants"`
		} `json:"items"`
	}
	if readJSON(ctx, source, "/api/v1/agent-links", &page) != nil {
		return false
	}
	for _, link := range page.Items {
		if link.Status != "connected" {
			continue
		}
		for _, item := range link.Assistants {
			if localui.LinkedAssistantID(link.ID, item.Address.AgentID) == owner.ID && item.Address.NodeID == owner.Remote && item.Address.Kind == "assistant" {
				return operation == "" || item.Capabilities.Supports(operation)
			}
		}
	}
	return false
}
