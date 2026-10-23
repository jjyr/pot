package transport

import (
	"context"
	"github.com/jjyr/pot/internal/control"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestCompletedTaskDownloadStreamsBeyondOldProxyLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	root := filepath.Join(t.TempDir(), "runtime")
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	runtime, err := product.Open(ctx, product.Options{Root: root, DisableMetadataRefresh: true, Control: control.Options{Keys: keys}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	s := runtime.Control
	owner, err := s.Registry.Create(ctx, "create", "Files")
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	profile, err := s.Profiles.Save(ctx, control.SaveProfile{RequestID: "model", Name: "Model", Model: "fixture", Endpoint: model.URL, Protocol: "openai_chat", APIKey: "isolated", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.Permissions.Configure(runtime.Root.Identity.RuntimeID, control.MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	task, err := s.Tasks.AcceptLocal(ctx, ownerID, protocol.CreateTask{RequestID: "artifact", Text: "Artifact fixture"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		record, err := s.Tasks.Get(ctx, ownerID, task.Info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Info.Phase == protocol.TaskComplete {
			break
		}
		if record.Info.Phase == protocol.TaskFailed {
			t.Fatal(record.Error)
		}
		if _, err = sub.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := s.RunJournal.TaskWorkspace(ctx, task.Info.ID)
	if err != nil || workspace == nil {
		t.Fatal(workspace, err)
	}
	directory := workspace.Path
	if err = os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(directory, "large.bin"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	const size = 33*1024*1024 + 4
	if err = file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteAt([]byte("TAIL"), size-4); err != nil {
		t.Fatal(err)
	}
	file.Close()
	upstream := httptest.NewServer(Handler(&Application{Control: s, RuntimeID: runtime.Root.Identity.RuntimeID}, "operator"))
	defer upstream.Close()
	config := webui.NewConfig()
	browser, err := webui.New(webui.Options{Origin: "http://localhost", Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "operator", Client: upstream.Client()}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	login := httptest.NewRequest("POST", "http://localhost/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	logged := httptest.NewRecorder()
	browser.ServeHTTP(logged, login)
	if logged.Code != 200 {
		t.Fatal(logged.Code)
	}
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Host = "localhost"; browser.ServeHTTP(w, r) }))
	defer public.Close()
	request, _ := http.NewRequestWithContext(ctx, "GET", public.URL+"/api/v1/tasks/"+task.Info.ID+"/file-download?assistant_id="+ownerID+"&path=large.bin", nil)
	request.AddCookie(logged.Result().Cookies()[0])
	response, err := public.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.ContentLength != size || !strings.Contains(response.Header.Get("Content-Disposition"), "large.bin") {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 1000))
		t.Fatal(response.StatusCode, response.Header, string(data))
	}
	if _, err = io.CopyN(io.Discard, response.Body, size-4); err != nil {
		t.Fatal(err)
	}
	tail, err := io.ReadAll(response.Body)
	if err != nil || string(tail) != "TAIL" {
		t.Fatal(string(tail), err)
	}
	request.URL.RawQuery = "assistant_id=someone-else&path=large.bin"
	response, err = public.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatal("artifact leaked to another owner")
	}
}
