package preview

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestPreviewRejectsCrossOriginAndInvalidRequests(t *testing.T) {
	s := New(context.Background())
	t.Cleanup(s.Close)
	handler := Handler(s, fstest.MapFS{"index.html": {Data: []byte("preview")}})
	for _, tc := range []struct {
		name, origin, host string
		want               int
	}{
		{"missing origin", "", "127.0.0.1:17702", 403},
		{"cross origin", "https://other.example", "127.0.0.1:17702", 403},
		{"same origin wrong scheme", "https://127.0.0.1:17702", "127.0.0.1:17702", 403},
		{"DNS rebinding", "http://other.example:17702", "other.example:17702", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tc.host+"/api/preview/ws", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body)
			}
		})
	}
}

func TestPreviewLegacyEndpointsRemoved(t *testing.T) {
	s := New(context.Background())
	t.Cleanup(s.Close)
	handler := Handler(s, fstest.MapFS{})
	for _, path := range []string{"/api/preview/snapshot", "/api/preview/messages", "/api/preview/events"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:17702"+path, nil)
		r.Host = "127.0.0.1:17702"
		r.Header.Set("Origin", "http://127.0.0.1:17702")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s status %d, want 404", path, w.Code)
		}
	}
}
