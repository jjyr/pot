package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/protocol"
)

var executionObserved = errors.New("execution observed")
var executionResubscribe = errors.New("execution stream requires a fresh snapshot")

type executionReply struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type executionResult struct {
	protocol.AgentExecutionStatus
	Messages []executionReply `json:"messages"`
}

// The status stream provides facts; this client's exit policy additionally
// requires the conversation stream to cross the same boot/cursor barrier.
func waitExperiment(ctx context.Context, o experimentOptions, request string, api experimentAPI, streams experimentStreams, out, diagnostics io.Writer) error {
	printed := map[string]bool{}
	lastProgress := ""
	var result executionResult
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("stopped waiting; submitted work remains active: %w", err)
		}
		var status *protocol.AgentExecutionStatus
		boot, conversation := "", ""
		cursor := int64(0)
		messages := map[string]protocol.Message{}
		var callbackErr error
		check := func() error {
			if status == nil || boot != status.BootID || conversation != status.ConversationID {
				return nil
			}
			stop := executionShouldStop(*status)
			caughtUp := cursor >= status.ConversationCursor
			replies := []executionReply{}
			for _, id := range status.ReplyIDs {
				m, ok := messages[id]
				if (!ok || m.Status != protocol.Complete) && stop && caughtUp {
					// A reset contains a bounded tail. Hydrate omitted replies through the
					// existing history resource, never through the execution-status stream.
					raw, err := api(ctx, o.Assistant, "GET", "/api/v1/history?around="+url.QueryEscape(id)+"&limit=1", nil)
					if err != nil {
						return err
					}
					var page protocol.UIPage
					if err = json.Unmarshal(raw, &page); err != nil {
						return err
					}
					if page.BootID != boot || page.ConversationID != conversation || page.Cursor < status.ConversationCursor {
						return executionResubscribe
					}
					for _, v := range page.Messages {
						messages[v.ID] = v.Message
					}
					m, ok = messages[id]
					if !ok || m.Role != protocol.Assistant || m.Status != protocol.Complete {
						return fmt.Errorf("completed reply %s unavailable in conversation history", id)
					}
				}
				if !ok || m.Status != protocol.Complete {
					break
				}
				if m.Role != protocol.Assistant {
					return errors.New("execution reply is not an assistant message")
				}
				replies = append(replies, executionReply{ID: id, Text: m.Text})
				if !o.JSON && !printed[id] {
					if _, err := fmt.Fprintln(out, m.Text); err != nil {
						return err
					}
					printed[id] = true
				}
			}
			if stop && caughtUp && len(replies) == len(status.ReplyIDs) {
				result = executionResult{AgentExecutionStatus: *status, Messages: replies}
				return executionObserved
			}
			return nil
		}
		err := streams(ctx, o.Assistant, request, func(event localui.Event) error {
			handle := func() error {
				switch event.Type {
				case "execution_status":
					var next protocol.AgentExecutionStatus
					if err := json.Unmarshal(event.Data, &next); err != nil {
						return err
					}
					if next.AssistantID != o.Assistant || next.RequestID != request || next.BootID == "" || next.ConversationID == "" || next.ConversationCursor < 0 {
						return errors.New("execution status identity mismatch")
					}
					status = &next
					progress := executionProgress(next)
					if progress != lastProgress {
						if _, err := fmt.Fprintln(diagnostics, progress); err != nil {
							return err
						}
						lastProgress = progress
					}
				case "conversation":
					var frame protocol.UIFrame
					if err := json.Unmarshal(event.Data, &frame); err != nil {
						return err
					}
					if frame.Kind == "reset" {
						boot, conversation, cursor = frame.BootID, frame.ConversationID, frame.Cursor
						messages = map[string]protocol.Message{}
					} else if frame.Kind == "delta" {
						if frame.BootID != boot || frame.ConversationID != conversation || frame.BaseCursor != cursor || frame.Cursor < cursor {
							return executionResubscribe
						}
						cursor = frame.Cursor
					} else {
						return errors.New("unknown conversation frame")
					}
					for _, m := range frame.UISnapshot.Messages {
						messages[m.ID] = m.Message
					}
				default:
					return errors.New("unexpected execution subscription event")
				}
				return check()
			}
			callbackErr = handle()
			return callbackErr
		})
		if err == nil {
			err = io.EOF
		}
		if errors.Is(err, executionObserved) {
			if o.JSON {
				if err = printUserJSON(out, result); err != nil {
					return err
				}
			}
			if result.State != "completed" {
				return fmt.Errorf("execution stopped: %s %s", result.State, result.Error)
			}
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("stopped waiting; submitted work remains active: %w", ctx.Err())
		}
		if callbackErr != nil && !errors.Is(callbackErr, executionResubscribe) {
			return callbackErr
		}
		var failure protocol.APIError
		if errors.As(err, &failure) {
			return err
		}
		if attempt == 3 {
			return fmt.Errorf("execution subscription disconnected; submitted work remains active: %w", err)
		}
		if _, e := fmt.Fprintln(diagnostics, "connection lost; reconnecting"); e != nil {
			return e
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("stopped waiting; submitted work remains active: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return errors.New("execution subscription ended")
}

func executionShouldStop(s protocol.AgentExecutionStatus) bool {
	if s.Terminal {
		return true
	}
	switch s.State {
	case "waiting", "waiting_approval", "paused", "remote_waiting", "unknown":
		return true
	}
	return false
}

func executionProgress(s protocol.AgentExecutionStatus) string {
	text := "execution=" + s.State + " assistant=" + s.Assistant.State
	if len(s.Assistant.ActiveTools) > 0 {
		text += " tools=" + strings.Join(s.Assistant.ActiveTools, ",")
	}
	for _, task := range s.Tasks {
		text += " task=" + task.ID + ":" + task.State
		if len(task.ActiveTools) > 0 {
			text += "(" + strings.Join(task.ActiveTools, ",") + ")"
		}
	}
	return text
}
