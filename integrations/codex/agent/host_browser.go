package agent

import (
	"net/http"
	"strings"
	"time"
)

// Browser handoffs never disclose the durable owner credential. A page token is
// short-lived and accepted only by the local connection-management routes.
func (h *agentHost) browserPage() map[string]any {
	h.browserMu.Lock()
	defer h.browserMu.Unlock()
	now := time.Now()
	for token, expiry := range h.browserTokens {
		if !expiry.After(now) {
			delete(h.browserTokens, token)
		}
	}
	if h.browserTokens == nil {
		h.browserTokens = make(map[string]time.Time)
	}
	// Bound abandoned pages without evicting the most recently opened one.
	if len(h.browserTokens) >= 32 {
		var oldest string
		for token, expiry := range h.browserTokens {
			if oldest == "" || expiry.Before(h.browserTokens[oldest]) {
				oldest = token
			}
		}
		delete(h.browserTokens, oldest)
	}
	token := identifier()
	expires := now.Add(30 * time.Minute)
	h.browserTokens[token] = expires
	return map[string]any{"url": "http://" + hostListen + "/codex-connections.html#" + token, "expires_at_ms": expires.UnixMilli()}
}

func (h *agentHost) browserOwner(r *http.Request) bool {
	if r.URL.Path != "/api/v1/agent-links" && r.URL.Path != "/api/v1/agent-links/invitation" {
		return false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	h.browserMu.Lock()
	defer h.browserMu.Unlock()
	expiry, ok := h.browserTokens[token]
	return ok && expiry.After(time.Now())
}
