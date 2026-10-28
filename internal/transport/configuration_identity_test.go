package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestConfigurationDispatchFencesOldControlAndBootBeforeMutation(t *testing.T) {
	writes := 0
	app := &Application{Control: &control.Service{}, RuntimeID: "synthetic-control", BootID: "synthetic-boot", RuntimeResources: func(context.Context, string, json.RawMessage) (any, error) {
		writes++
		return map[string]bool{"saved": true}, nil
	}}
	const path = "/api/v1/runtime/resources?assistant_id=alpha"
	const current = "&configuration_control_id=synthetic-control&configuration_boot_id=synthetic-boot"
	for _, suffix := range []string{
		"&configuration_control_id=old-control&configuration_boot_id=synthetic-boot",
		"&configuration_control_id=synthetic-control&configuration_boot_id=old-boot",
		"&configuration_control_id=synthetic-control",
		"&configuration_boot_id=synthetic-boot",
	} {
		_, err := app.Call(t.Context(), Request{Method: "POST", Path: path + suffix, Body: json.RawMessage(`{}`)})
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != "identity_changed" || writes != 0 {
			t.Fatal("old native form reached mutation", writes, err)
		}
		request := httptest.NewRequest(http.MethodPost, path+suffix, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer synthetic-token")
		response := httptest.NewRecorder()
		Handler(app, "synthetic-token").ServeHTTP(response, request)
		if response.Code != http.StatusConflict || writes != 0 {
			t.Fatal("old HTTP form reached mutation", response.Code, writes, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, path+current, strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer synthetic-token")
	response := httptest.NewRecorder()
	Handler(app, "synthetic-token").ServeHTTP(response, request)
	if response.Code != http.StatusOK || writes != 1 {
		t.Fatal("current configuration was not saved", response.Code, writes, response.Body.String())
	}
	app.BootID = "replacement-boot"
	if _, err := app.Call(t.Context(), Request{Method: "POST", Path: path + current, Body: json.RawMessage(`{}`)}); err == nil || writes != 1 {
		t.Fatal("late old boot wrote replacement", writes, err)
	}
}
