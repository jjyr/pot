package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"strings"
	"testing"
)

func TestSignedLargePayloadRoundTrip(t *testing.T) {
	handler := func(_ context.Context, _ Principal, r Request) (json.RawMessage, error) { return r.Params, nil }
	control, _, _ := pair(t, nil, handler)
	peer := waitPeer(t, control, "worker")
	raw, _ := json.Marshal(strings.Repeat("long context 世界 ", 180000))
	reply, err := peer.Call(context.Background(), Request{"large", "test.large", raw})
	if err != nil || string(reply) != string(raw) {
		t.Fatalf("large payload round trip: got %d expected %d error %v", len(reply), len(raw), err)
	}
}
func TestFragmentBoundariesRejectChangedOffsetsAndBudget(t *testing.T) {
	p := &Peer{fragments: map[string]*assembly{}}
	header := json.RawMessage(`{"size":200000,"digest":"0000000000000000000000000000000000000000000000000000000000000000"}`)
	if _, err := p.receiveFragment(wire{Kind: "request.start", ID: "id", Method: "model", Params: header}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.receiveFragment(wire{Kind: "request.chunk", ID: "id", Method: "model", Params: json.RawMessage(`{"offset":1,"data":"aGk"}`)}); err == nil {
		t.Fatal("changed offset accepted")
	}
	oversize := json.RawMessage(`{"size":999999999,"digest":"0000000000000000000000000000000000000000000000000000000000000000"}`)
	if _, err := p.receiveFragment(wire{Kind: "request.start", ID: "too-big", Method: "model", Params: oversize}); err == nil {
		t.Fatal("large allocation admitted")
	}
}
func TestRemoteKnownUnsentAndAllowanceMarkers(t *testing.T) {
	handler := func(_ context.Context, _ Principal, r Request) (json.RawMessage, error) {
		return nil, &core.ModelRequestError{Cause: protocol.APIError{Code: "pot_token_limit", Message: "waiting"}, Disposition: "unsent"}
	}
	_, _, peer := pair(t, handler, nil)
	_, err := peer.Call(context.Background(), Request{"allowance", "test.allowance", json.RawMessage(`{}`)})
	if !core.IsModelRequestUnsent(err) {
		t.Fatalf("known unsent marker missing: %v", err)
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != "pot_token_limit" {
		t.Fatalf("allowance marker missing: %v", err)
	}
}
