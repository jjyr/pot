package preview

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func fixture(t *testing.T) *Service {
	t.Helper()
	s := New(context.Background())
	t.Cleanup(s.Close)
	return s
}

func TestDuplicateSendDoesNotStartAnotherReply(t *testing.T) {
	s := fixture(t)
	request := SendRequest{RequestID: "send-1", BootID: s.boot, ConversationID: ConversationID, Text: "hello"}
	first, err := s.Send(request)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.Send(request)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.MessageID != first.MessageID || !repeated.Duplicate || len(s.Snapshot().Messages) != 4 {
		t.Fatalf("duplicate changed the accepted run: %#v", repeated)
	}
	request.Text = "different"
	if _, err := s.Send(request); HTTPStatus(err) != http.StatusConflict {
		t.Fatalf("conflicting identity was accepted: %v", err)
	}
}

func TestCancellationAndSlowSubscriberSeeFinalState(t *testing.T) {
	s := fixture(t)
	updates, remove := s.Subscribe()
	defer remove()
	if _, err := s.Send(SendRequest{RequestID: "send-1", BootID: s.boot, ConversationID: ConversationID, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(CancelRequest{RequestID: "cancel-1", BootID: s.boot, TurnID: "send-1"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	select {
	case last := <-updates:
		if last.Running || last.Messages[len(last.Messages)-1].Status != "cancelled" {
			t.Fatalf("slow subscriber missed final state: %#v", last)
		}
	case <-time.After(time.Second):
		t.Fatal("no final snapshot")
	}
	if _, err := s.Send(SendRequest{RequestID: "send-2", BootID: s.boot, ConversationID: ConversationID, Text: "after shutdown"}); HTTPStatus(err) != 503 {
		t.Fatalf("closed preview accepted new work: %v", err)
	}
}

func TestDelayedCancellationCannotStopLaterReply(t *testing.T) {
	s := fixture(t)
	_, err := s.Send(SendRequest{RequestID: "send-1", BootID: s.boot, ConversationID: ConversationID, Text: "one"})
	if err != nil {
		t.Fatal(err)
	}
	stop := CancelRequest{RequestID: "stop-1", BootID: s.boot, TurnID: "send-1"}
	if err := s.Cancel(stop); err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	_, err = s.Send(SendRequest{RequestID: "send-2", BootID: s.boot, ConversationID: ConversationID, Text: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(stop); err != nil {
		t.Fatal(err)
	}
	stop.RequestID = "another-stop"
	if err := s.Cancel(stop); HTTPStatus(err) != http.StatusConflict {
		t.Fatalf("stale cancellation accepted: %v", err)
	}
	snapshot := s.Snapshot()
	if !snapshot.Running || snapshot.TurnID == nil || *snapshot.TurnID != "send-2" {
		t.Fatal("delayed stop affected later reply")
	}
	_, err = s.Send(SendRequest{RequestID: "send-3", BootID: "old-boot", ConversationID: ConversationID, Text: "three"})
	if HTTPStatus(err) != http.StatusConflict {
		t.Fatalf("stale boot accepted: %v", err)
	}
}

func TestPreviewHTTPRejectsCrossOriginAndInvalidRequests(t *testing.T) {
	handler := Handler(fixture(t), fstest.MapFS{"index.html": {Data: []byte("preview")}})
	for _, tc := range []struct {
		name, body, origin, host string
		want                     int
	}{
		{"cross origin", `{}`, "https://other.example", "127.0.0.1:17702", 403},
		{"DNS rebinding", `{}`, "", "other.example:17702", 403},
		{"unknown field", `{"request_id":"r","conversation_id":"preview-conversation","text":"hello","target":"host"}`, "", "127.0.0.1:17702", 400},
		{"multiple bodies", `{} {}`, "", "127.0.0.1:17702", 400},
		{"wrong conversation", `{"request_id":"r","conversation_id":"another","text":"hello"}`, "", "127.0.0.1:17702", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1:17702/api/preview/messages", strings.NewReader(tc.body))
			r.Host = tc.host
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body)
			}
		})
	}
}

func TestSSEInitialSnapshotAndDisconnectReleaseSubscription(t *testing.T) {
	s := fixture(t)
	server := httptest.NewServer(Handler(s, fstest.MapFS{}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/preview/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	if event, _ := reader.ReadString('\n'); event != "event: snapshot\n" {
		t.Fatalf("event: %q", event)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var snapshot Snapshot
	if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ConversationID != ConversationID || snapshot.Cursor != "0" {
		t.Fatalf("snapshot: %#v", snapshot)
	}
	cancel()
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		remaining := len(s.subscribers)
		s.mu.Unlock()
		if remaining == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("disconnect retained subscription")
		case <-time.After(time.Millisecond):
		}
	}
}
