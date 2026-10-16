package transport

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/resource"
	"github.com/jjyr/pot/pkg/resourcecontrol"
	"github.com/jjyr/pot/pkg/sandbox"
	"github.com/jjyr/pot/pkg/sandboxresources"
)

func TestResourceOwnerAPIRequiresAuthenticationAndExplicitAction(t *testing.T) {
	catalog, err := sandboxresources.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	store, err := resource.New(filepath.Join(t.TempDir(), "cache"), resource.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(context.Background())
	manager, err := resourcecontrol.Open(resourcecontrol.Options{Root: t.TempDir(), Catalog: catalog, Store: store, Bind: func(context.Context, string, sandbox.PreparedResources) error {
		t.Fatal("unprepared resources bound")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	handler := Handler(&Application{Control: &control.Service{}, Resources: manager}, "owner-token")
	invoke := func(method, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://runtime/api/v1/sandbox-resources", strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := invoke("POST", `{"action":"download","request_id":"unknown"}`, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := invoke("GET", "", "owner-token"); w.Code != 200 || !strings.Contains(w.Body.String(), `"requests":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := invoke("POST", `{"action":"request","sandbox_id":"shared"}`, "owner-token"); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"awaiting_consent"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := invoke("POST", `{"action":"download","request_id":"unknown","consent":true}`, "owner-token"); w.Code < 400 {
		t.Fatal("unknown/model consent field accepted")
	}
}
