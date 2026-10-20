package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
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
