package transport

import (
	"encoding/json"
	"github.com/jjyr/pot/internal/spaces"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/schedule"
)

func TestSchedulePreviewHTTP(t *testing.T) {
	app := historyApplication(t, 0)
	handler := Handler(app, "private")
	for _, test := range []struct{ body, field string }{
		{`{"kind":"cron","cron":"0 8-20 * * *","timezone":"Europe/Berlin"}`, ""},
		{`{"kind":"cron","cron":"0 8-20 * * * *","timezone":"Europe/Berlin"}`, "cron"},
		{`{"kind":"cron","cron":"0 8 * * *","timezone":"Local"}`, "timezone"},
		{`{"kind":"cron","cron":"0 0 31 FEB *","timezone":"UTC"}`, "cron"},
	} {
		r := httptest.NewRequest("POST", "/api/v1/schedules/preview?assistant_id=main", strings.NewReader(test.body))
		r.Header.Set("Authorization", "Bearer private")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var result schedule.PreviewResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Field != test.field {
			t.Fatal(w.Code, w.Body.String())
		}
		if test.field == "" && len(result.Times) != 5 {
			t.Fatal(result)
		}
	}
	remoteServer := httptest.NewServer(http.StripPrefix("/control/v1/owner", handler))
	defer remoteServer.Close()
	remote, err := spaces.NewHTTPClient(remoteServer.URL, "private", "", "history-fixture")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := remote.Call(t.Context(), "POST", "/api/v1/schedules/preview?assistant_id=main&space_id=remote", json.RawMessage(`{"kind":"cron","cron":"0 8-20 * * *","timezone":"Pacific/Auckland"}`))
	var preview schedule.PreviewResult
	if err != nil || json.Unmarshal(raw, &preview) != nil || preview.Timezone != "Pacific/Auckland" || len(preview.Times) != 5 {
		t.Fatal(string(raw), err)
	}
	page, err := app.Control.Schedules.Page(t.Context(), "main", schedule.Query{})
	if err != nil || page.Total != 0 {
		t.Fatal("preview wrote schedules", page, err)
	}
	r := httptest.NewRequest("POST", "/api/v1/schedules/preview?assistant_id=main", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
