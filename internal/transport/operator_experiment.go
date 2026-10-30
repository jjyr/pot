package transport

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/usage"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// These routes are private operator commands and projections, absent from the browser allowlist.
func (a *Application) operatorExperimentCall(ctx context.Context, method string, u *url.URL, r Request) (any, error, bool) {
	if !strings.HasPrefix(u.Path, "/api/v1/operator/") {
		return nil, nil, false
	}
	if u.Path == "/api/v1/operator/ask" && method == "POST" {
		var request control.AskRequest
		if err := decode(r.Body, &request); err != nil {
			return nil, err, true
		}
		value, err := a.Control.Ask(ctx, u.Query().Get("assistant_id"), request)
		return value, err, true
	}
	if method != "GET" {
		return nil, errors.New("GET required"), true
	}
	q := u.Query()
	switch u.Path {
	case "/api/v1/operator/usage":
		offset := 0
		if q.Has("offset") {
			n, e := strconv.Atoi(q.Get("offset"))
			if e != nil {
				return nil, e, true
			}
			offset = n
		}
		details := false
		if q.Has("details") {
			v, e := strconv.ParseBool(q.Get("details"))
			if e != nil {
				return nil, e, true
			}
			details = v
		}
		v, e := a.Control.ExperimentUsage(ctx, q.Get("assistant_id"), q.Get("task_id"), usage.Query{Start: q.Get("start"), End: q.Get("end"), Timezone: q.Get("timezone"), Turn: q.Get("turn"), Offset: offset}, details, time.Now().UnixMilli())
		return v, e, true
	default:
		return nil, errors.New("unknown operator query"), true
	}
}
