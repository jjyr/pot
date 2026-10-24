package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/core"
)

func TestTransientProviderFailures(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 408, 413, 422, 429, 500, 501, 502, 503, 504} {
		err := StatusError(status)
		var transient *core.TransientModelError
		want := status == 408 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504
		if errors.As(err, &transient) != want {
			t.Fatal("incorrect provider status retry classification", status, err)
		}
	}
	for _, failure := range []error{io.EOF, io.ErrUnexpectedEOF, net.ErrClosed, syscall.ECONNRESET, syscall.ECONNREFUSED} {
		var transient *core.TransientModelError
		if !errors.As(connectionError(failure), &transient) {
			t.Fatal("lost transient network failure", failure)
		}
	}
	for _, failure := range []error{context.Canceled, errors.New("certificate verification failed"), &net.DNSError{Err: "no such host", IsNotFound: true}} {
		var transient *core.TransientModelError
		if errors.As(connectionError(failure), &transient) {
			t.Fatal("permanent failure marked transient", failure)
		}
	}
}

func TestInterruptedHTTPBodyIsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, "data: partial\n\n")
	}))
	defer server.Close()
	response, err := Forward(context.Background(), provider(server.URL, "openai_chat"), []byte("fixture"), json.RawMessage(`{}`), "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	var transient *core.TransientModelError
	if !errors.As(err, &transient) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("stream interruption lost its retry marker/diagnostic", err)
	}
}

func TestForwardDoesNotLimitActiveResponseDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for range 8 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(25 * time.Millisecond):
				fmt.Fprint(w, ": active\n\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 5*time.Second))
	defer cancel()
	response, err := Forward(ctx, provider(server.URL, "openai_chat"), []byte("fixture"), json.RawMessage(`{}`), "", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || len(data) != 8*len(": active\n\n") {
		t.Fatal("HTTP client cut off an active stream", string(data), err)
	}
}
