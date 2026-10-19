package bridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func clientConfig(path string) (ClientConfig, *http.Client, error) {
	var c ClientConfig
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, nil, err
	}
	if len(raw) > 16384 || json.Unmarshal(raw, &c) != nil {
		return c, nil, errors.New("invalid_mcp_pairing")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !(u.Path == "" || u.Path == "/") || !(u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) || len(c.Token) < 32 {
		return c, nil, errors.New("invalid_mcp_endpoint_or_token")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return c, nil, err
	}
	if c.CA != "" && !roots.AppendCertsFromPEM([]byte(c.CA)) {
		return c, nil, errors.New("invalid_ca")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	return c, &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_denied") }}, nil
}
func mcpTools() []any {
	schema := func(props map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	text := func(n int) map[string]any { return map[string]any{"type": "string", "maxLength": n} }
	props := map[string]any{"request_id": text(128), "body": text(12000), "correlation": text(128), "depth": map[string]any{"type": "integer", "minimum": 0, "maximum": 1}}
	return []any{
		map[string]any{"name": "pot_notify", "description": "Send reference data to the locally paired Pot assistant. Accepted is not delivered or executed. Do not forward automatically or include secrets.", "inputSchema": schema(props, []string{"request_id", "body"})},
		map[string]any{"name": "pot_request", "description": "Request help from the paired Pot assistant. It arrives as reference data; execution needs the user's authorization. Query its receipt/result with pot_receipt; no automatic delegation loop.", "inputSchema": schema(props, []string{"request_id", "body"})},
		map[string]any{"name": "pot_receipt", "description": "Query a Pot notification/request receipt: accepted, received by the durable inbox, or explicitly answered. Received is not assistant completion.", "inputSchema": schema(map[string]any{"message_id": text(128)}, []string{"message_id"})},
	}
}

// MCP speaks the official newline-delimited stdio transport. Stdout contains
// only JSON-RPC; it never starts a model or attaches to an App conversation.
func MCP(in io.Reader, out io.Writer, path string) error {
	c, client, err := clientConfig(path)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxBody+1)
	encoder := json.NewEncoder(out)
	initialized := false
	for scanner.Scan() {
		if len(scanner.Bytes()) > maxBody {
			return errors.New("mcp_request_limit")
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			return errors.New("invalid_mcp_json")
		}
		if len(req.ID) == 0 {
			continue
		}
		var result any
		var rpcErr any
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			version := p.ProtocolVersion
			if version != "2025-06-18" && version != "2025-03-26" && version != "2024-11-05" {
				version = "2025-06-18"
			}
			initialized = true
			result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "pot", "version": "0.1.0"}}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			if initialized {
				result = map[string]any{"tools": mcpTools()}
			} else {
				rpcErr = map[string]any{"code": -32000, "message": "Initialize first"}
			}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if !initialized || json.Unmarshal(req.Params, &p) != nil {
				rpcErr = map[string]any{"code": -32602, "message": "Invalid tool call"}
				break
			}
			var args struct {
				RequestID   string `json:"request_id"`
				Body        string `json:"body"`
				Correlation string `json:"correlation"`
				Depth       int    `json:"depth"`
				MessageID   string `json:"message_id"`
			}
			d := json.NewDecoder(bytes.NewReader(p.Arguments))
			d.DisallowUnknownFields()
			if d.Decode(&args) != nil {
				rpcErr = map[string]any{"code": -32602, "message": "Invalid arguments"}
				break
			}
			route := "/v1/messages"
			method := "POST"
			var payload any
			switch p.Name {
			case "pot_notify", "pot_request":
				if !validID(args.RequestID) || args.Body == "" || len(args.Body) > 12000 || len(args.Correlation) > 128 || args.Depth < 0 || args.Depth > 1 || args.MessageID != "" {
					rpcErr = map[string]any{"code": -32602, "message": "Invalid message bounds"}
					break
				}
				kind := strings.TrimPrefix(p.Name, "pot_")
				payload = map[string]any{"request_id": args.RequestID, "kind": kind, "body": args.Body, "correlation": args.Correlation, "depth": args.Depth}
			case "pot_receipt":
				if !validID(args.MessageID) || args.RequestID != "" || args.Body != "" || args.Correlation != "" || args.Depth != 0 {
					rpcErr = map[string]any{"code": -32602, "message": "Invalid receipt arguments"}
					break
				}
				route += "/" + args.MessageID
				method = "GET"
			default:
				rpcErr = map[string]any{"code": -32601, "message": "Unknown tool"}
			}
			if rpcErr != nil {
				break
			}
			var data io.Reader
			if payload != nil {
				data = strings.NewReader(encoded(payload))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			request, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Endpoint, "/")+route, data)
			var content string
			failed := false
			if e == nil {
				request.Header.Set("Authorization", "Bearer "+c.Token)
				request.Header.Set("Content-Type", "application/json")
				var response *http.Response
				response, e = client.Do(request)
				if e == nil {
					raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
					response.Body.Close()
					if readErr != nil || len(raw) > maxBody {
						e = errors.New("bridge_response_limit")
					} else {
						content = string(raw)
						failed = response.StatusCode != 200 && response.StatusCode != 202
					}
				}
			}
			cancel()
			if e != nil {
				content = "bridge_unreachable_check_daemon_address_tls_pairing"
				failed = true
			}
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": content}}, "isError": failed}
		default:
			rpcErr = map[string]any{"code": -32601, "message": "Unknown method"}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		if err = encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}
