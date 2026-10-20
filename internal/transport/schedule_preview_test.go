package transport

import (
	"encoding/json"
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
