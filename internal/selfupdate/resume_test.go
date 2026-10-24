package selfupdate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type heldResumeResponse struct {
	header       http.Header
	body         bytes.Buffer
	writing      chan struct{}
	flushing     chan struct{}
	writeAllowed chan struct{}
	flushAllowed chan struct{}
	writeOnce    sync.Once
	flushOnce    sync.Once
	flushErr     error
}

func (w *heldResumeResponse) Header() http.Header { return w.header }
func (w *heldResumeResponse) WriteHeader(int)     {}
func (w *heldResumeResponse) Write(body []byte) (int, error) {
	close(w.writing)
	<-w.writeAllowed
	return w.body.Write(body)
}
func (w *heldResumeResponse) FlushError() error {
	close(w.flushing)
	<-w.flushAllowed
	return w.flushErr
}
func (w *heldResumeResponse) allowWrite() { w.writeOnce.Do(func() { close(w.writeAllowed) }) }
func (w *heldResumeResponse) allowFlush() { w.flushOnce.Do(func() { close(w.flushAllowed) }) }

func TestHostResumeAcknowledgesBeforeRestart(t *testing.T) {
	for _, flushErr := range []error{nil, errors.New("fixture flush failed")} {
		name := "flushed"
		if flushErr != nil {
			name = "flush_failure"
		}
		t.Run(name, func(t *testing.T) {
			host := &Host{resume: make(chan struct{}, 1)}
			host.stopped.Store(true)
			response := &heldResumeResponse{header: make(http.Header), writing: make(chan struct{}), flushing: make(chan struct{}), writeAllowed: make(chan struct{}), flushAllowed: make(chan struct{}), flushErr: flushErr}
			done := make(chan struct{})
			go func() {
				defer close(done)
				host.handle(response, httptest.NewRequest(http.MethodPost, "http://update/resume", nil))
			}()
			t.Cleanup(func() {
				response.allowWrite()
				response.allowFlush()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("resume handler did not stop")
				}
			})
			wait := func(signal <-chan struct{}) {
				t.Helper()
				select {
				case <-signal:
				case <-time.After(5 * time.Second):
					t.Fatal("resume response did not reach its barrier")
				}
			}
			assertStopped := func(stage string) {
				t.Helper()
				select {
				case <-host.resume:
					t.Fatalf("restart triggered while %s", stage)
				default:
				}
			}
			wait(response.writing)
			assertStopped("response write was blocked")
			response.allowWrite()
			wait(response.flushing)
			assertStopped("response flush was blocked")
			response.allowFlush()
			wait(done)
			var result map[string]bool
			if err := json.Unmarshal(response.body.Bytes(), &result); err != nil || !result["accepted"] {
				t.Fatal("missing resume acknowledgement", result, err)
			}
			if flushErr != nil {
				assertStopped("response flush failed")
				return
			}
			select {
			case <-host.resume:
			default:
				t.Fatal("acknowledged resume did not restart the host")
			}
		})
	}
}

func TestNativeExitRequiresCleanupAndFlushedResponse(t *testing.T) {
	for _, flushErr := range []error{nil, errors.New("fixture flush failed")} {
		t.Run(fmt.Sprint(flushErr), func(t *testing.T) {
			quit := make(chan struct{}, 1)
			host := &Host{done: make(chan struct{})}
			host.AppLifecycle(func() error { return nil }, func() { quit <- struct{}{} })
			premature := httptest.NewRecorder()
			host.handle(premature, httptest.NewRequest("POST", "http://update/exit", nil))
			if premature.Code != 409 {
				t.Fatal("unprepared App was allowed to exit")
			}
			prepared := httptest.NewRecorder()
			host.handle(prepared, httptest.NewRequest("POST", "http://update/prepare", nil))
			if prepared.Code != 200 {
				t.Fatal(prepared.Body.String())
			}
			response := &heldResumeResponse{header: make(http.Header), writing: make(chan struct{}), flushing: make(chan struct{}), writeAllowed: make(chan struct{}), flushAllowed: make(chan struct{}), flushErr: flushErr}
			done := make(chan struct{})
			go func() {
				defer close(done)
				host.handle(response, httptest.NewRequest("POST", "http://update/exit", nil))
			}()
			t.Cleanup(func() { response.allowWrite(); response.allowFlush(); <-done })
			wait := func(ch <-chan struct{}) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(5 * time.Second):
					t.Fatal("exit response barrier timed out")
				}
			}
			stopped := func() {
				t.Helper()
				select {
				case <-quit:
					t.Fatal("App exited without a flushed acknowledgement")
				default:
				}
			}
			wait(response.writing)
			stopped()
			response.allowWrite()
			wait(response.flushing)
			stopped()
			response.allowFlush()
			wait(done)
			if flushErr != nil {
				stopped()
				return
			}
			wait(quit)
		})
	}
}

func TestNativeCleanupFailureRefusesExit(t *testing.T) {
	host := &Host{done: make(chan struct{})}
	host.AppLifecycle(func() error { return errors.New("cleanup unconfirmed") }, func() { t.Error("App quit after failed cleanup") })
	for _, route := range []string{"prepare", "exit"} {
		response := httptest.NewRecorder()
		host.handle(response, httptest.NewRequest("POST", "http://update/"+route, nil))
		if response.Code != 409 {
			t.Fatal("accepted failed native cleanup", route, response.Code)
		}
	}
}
