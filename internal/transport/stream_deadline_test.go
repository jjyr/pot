package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(value time.Time) error {
	w.deadlines = append(w.deadlines, value)
	return nil
}

func TestStreamDeadlinesBoundWritesOnly(t *testing.T) {
	for _, heartbeat := range []bool{false, true} {
		w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		controller := http.NewResponseController(w)
		var err error
		if heartbeat {
			err = writeHeartbeat(controller, w)
		} else {
			err = writeSSE(controller, w, "snapshot", "boot:main:1", []byte(`{}`))
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(w.deadlines) != 2 || w.deadlines[0].IsZero() || !w.deadlines[1].IsZero() {
			t.Fatal("write deadline must be cleared before waiting for the next frame", w.deadlines)
		}
	}
}
