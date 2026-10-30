package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/protocol"
)

// SocketCaller adapts the preview Service to the localui router/Caller interface.
func SocketCaller(s *Service) localui.Caller {
	return localui.Functions{
		Request: func(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(body) > 65536 {
				return nil, &localui.SocketError{
					Status:   413,
					APIError: protocol.APIError{Code: "body_limit", Message: "Request body too large"},
				}
			}
			switch {
			case method == "GET" && path == "/api/preview/snapshot":
				return json.Marshal(s.Snapshot())
			case method == "POST" && path == "/api/preview/messages":
				var request SendRequest
				if err := strictDecode(body, &request); err != nil {
					return nil, err
				}
				receipt, err := s.Send(request)
				if err != nil {
					return nil, socketError(err)
				}
				return json.Marshal(receipt)
			case method == "POST" && path == "/api/preview/cancel":
				var request CancelRequest
				if err := strictDecode(body, &request); err != nil {
					return nil, err
				}
				if err := s.Cancel(request); err != nil {
					return nil, socketError(err)
				}
				return json.Marshal(map[string]bool{"accepted": true})
			default:
				return nil, notFound()
			}
		},
		Events: func(ctx context.Context, path string, emit func(localui.Event) error) error {
			if path != "/api/preview/events" {
				return notFound()
			}
			updates, stop := s.Subscribe()
			defer stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-s.ctx.Done():
					return nil
				case snapshot := <-updates:
					data, err := json.Marshal(snapshot)
					if err != nil {
						return err
					}
					if err := emit(localui.Event{Type: "snapshot", Data: data}); err != nil {
						return err
					}
				}
			}
		},
	}
}

func strictDecode(body json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &localui.SocketError{
			Status:   400,
			APIError: protocol.APIError{Code: "invalid_request", Message: "Invalid preview request"},
		}
	}
	if decoder.Decode(new(any)) != io.EOF {
		return &localui.SocketError{
			Status:   400,
			APIError: protocol.APIError{Code: "invalid_request", Message: "Expected a single request"},
		}
	}
	return nil
}

func socketError(err error) *localui.SocketError {
	e := asError(err)
	return &localui.SocketError{
		Status:   HTTPStatus(err),
		APIError: protocol.APIError{Code: e.Code, Message: e.Message},
	}
}

func notFound() *localui.SocketError {
	return &localui.SocketError{
		Status:   404,
		APIError: protocol.APIError{Code: "not_found", Message: "Not found"},
	}
}
