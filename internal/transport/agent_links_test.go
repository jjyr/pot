package transport

import (
	"context"
	"errors"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestAgentLinkRoutesDoNotExposeLegacyPairing(t *testing.T) {
	a := &Application{}
	for _, method := range []string{"GET", "POST"} {
		path := "/api/v1/agent-links/invitation"
		value, err, handled := a.agentLinkCall(context.Background(), method, path, Request{Method: method, Path: path})
		if handled || value != nil || err != nil {
			t.Fatalf("%s invitation unexpectedly handled: value=%v err=%v", method, value, err)
		}
	}
	path := "/api/v1/agent-links"
	_, err, handled := a.agentLinkCall(context.Background(), "POST", path, Request{Method: "POST", Path: path})
	var apiErr protocol.APIError
	if !handled || !errors.As(err, &apiErr) || apiErr.Code != "method_not_allowed" {
		t.Fatalf("legacy POST should be unsupported: handled=%v err=%v", handled, err)
	}
}
