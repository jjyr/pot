// Package mcppublic defines the complete public MCP HTTP surface. It contains no
// owner API route or configurable forwarding target.
package mcppublic

import "net/http"

func Route(path string) bool {
	switch path {
	case "/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server", "/oauth/register", "/oauth/authorize", "/oauth/consent", "/oauth/token", "/oauth/revoke", "/oauth/connections":
		return true
	}
	return false
}
func Method(path, method string) bool {
	switch path {
	case "/mcp":
		return method == http.MethodGet || method == http.MethodPost || method == http.MethodDelete
	case "/oauth/register", "/oauth/token", "/oauth/revoke":
		return method == http.MethodPost
	case "/oauth/consent", "/oauth/connections":
		return method == http.MethodGet || method == http.MethodPost
	default:
		return Route(path) && method == http.MethodGet
	}
}
