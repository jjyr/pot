package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/execution"
	webassets "github.com/jjyr/pot/web"
)

func resourceApplication(t *testing.T) (*Application, []resources.Resource) {
	t.Helper()
	ctx := context.Background()
	sources := []string{"landscape.png", "pulse.gif", "voice.wav", "sample.webm", "notes.pdf", "demo.html", "report.txt"}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			}
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		registered := map[string]resources.Resource{}
		presented := 0
		for _, message := range body.Messages {
			if message.Role != "tool" {
				continue
			}
			text, _ := message.Content.(string)
			var resource resources.Resource
			if json.Unmarshal([]byte(text), &resource) == nil && resource.ID != "" {
				registered[resource.Name] = resource
			}
			if strings.Contains(text, `"presentation"`) {
				presented++
			}
		}
		var calls []any
		add := func(name, id string, args any) {
			raw, _ := json.Marshal(args)
			calls = append(calls, map[string]any{"index": len(calls), "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}})
		}
		if len(registered) == 0 {
			for index, path := range sources {
				args := map[string]any{"path": path}
				if path == "demo.html" {
					args["assets"] = []string{"demo.css", "demo.js"}
				}
				add("register_resource", fmt.Sprintf("register-%d", index), args)
			}
		} else if presented == 0 {
			for index, path := range sources {
				mode := "preview"
				if path == "report.txt" {
					mode = "download"
				}
				add("present_resource", fmt.Sprintf("present-%d", index), map[string]string{"resource_id": registered[path].ID, "mode": mode, "caption": map[string]string{"landscape.png": "生成的图片", "pulse.gif": "GIF 动画", "voice.wav": "语音播报", "sample.webm": "视频演示", "notes.pdf": "PDF 文档", "demo.html": "预算模拟器 · 可直接调整参数", "report.txt": "导出文件"}[path]})
			}
		}
		delta := map[string]any{"content": "这些资源可以直接查看和操作，下载按钮单独保存原文件。"}
		reason := "stop"
		if len(calls) > 0 {
			delta = map[string]any{"tool_calls": calls}
			reason = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 20}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	t.Cleanup(provider.Close)
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("resource-fixture", "boot", execution.DefaultLimits())
	root := t.TempDir()
	service, err := control.Open(control.Options{Root: root, RuntimeID: "resource-fixture", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	if _, err = service.Registry.Create(ctx, "fixture", "资源展示"); err != nil {
		t.Fatal(err)
	}
	workspace, _ := service.ExecutionWorkspace("main")
	writeResourceFixtures(t, workspace)
	profile, err := service.Profiles.Save(ctx, control.SaveProfile{RequestID: "provider", Name: "Fixture", Model: "gpt-4.1", Endpoint: provider.URL + "/v1", Protocol: "openai_chat", APIKey: "isolated", MaxOutputTokens: 2000})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Rehash()
	c, err := service.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	app := &Application{Control: service, RuntimeID: "resource-fixture", BootID: "boot", Name: "Pot"}
	sub, remove, err := c.SubscribeUI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/messages", Body: json.RawMessage(`{"request_id":"resource-showcase","conversation_id":"main","text":"请展示图片、GIF、语音、视频、PDF 和可操作的 HTML，并单独给出下载文件。"}`)}); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		snapshot, err := c.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var output []resources.Resource
		for _, message := range snapshot.Messages {
			if message.Tool != nil && message.Tool.Name == "present_resource" && message.Tool.Phase == "completed" {
				var value struct {
					Presentation resources.Presentation `json:"presentation"`
				}
				json.Unmarshal(message.Tool.Result, &value)
				output = append(output, value.Presentation.Resource)
			}
		}
		if snapshot.Turn == nil && len(output) == len(sources) {
			presentations := 0
			for _, message := range snapshot.Messages {
				if message.Presentation != nil {
					presentations++
				}
			}
			catalogue, err := c.Catalogue(ctx)
			if err != nil || presentations != len(sources) || catalogue.ReplyCount != uint64(len(sources)+1) {
				t.Fatal("resource replies were not durable unread messages", presentations, catalogue, err)
			}
			return app, output
		}
		if snapshot.Turn == nil && len(snapshot.Messages) > 1 {
			t.Fatalf("resource tool turn failed: %+v", snapshot.Messages)
		}
		select {
		case <-sub:
		case <-timer.C:
			t.Fatal("resource fixture timed out")
		}
	}
}

func writeResourceFixtures(t *testing.T, workspace string) {
	t.Helper()
	save := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(workspace, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, 480, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 480; x++ {
			pixel := color.NRGBA{222, 236, 242, 255}
			if y > 180-int(50*math.Sin(float64(x)/90)) {
				pixel = color.NRGBA{66, 116, 103, 255}
			}
			if (x-390)*(x-390)+(y-55)*(y-55) < 28*28 {
				pixel = color.NRGBA{238, 195, 97, 255}
			}
			canvas.Set(x, y, pixel)
		}
	}
	var pngBytes bytes.Buffer
	png.Encode(&pngBytes, canvas)
	save("landscape.png", pngBytes.Bytes())
	animation := gif.GIF{LoopCount: 0}
	palette := color.Palette{color.NRGBA{233, 240, 234, 255}, color.NRGBA{76, 121, 103, 255}, color.NRGBA{235, 191, 91, 255}}
	for step := 0; step < 12; step++ {
		frame := image.NewPaletted(image.Rect(0, 0, 240, 90), palette)
		for y := 20; y < 70; y++ {
			for x := 15 + step*14; x < 45+step*14; x++ {
				frame.SetColorIndex(x, y, byte(1+step%2))
			}
		}
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 10)
	}
	var gifBytes bytes.Buffer
	gif.EncodeAll(&gifBytes, &animation)
	save("pulse.gif", gifBytes.Bytes())
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+24000*2))
	wav.WriteString("WAVEfmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint32(12000))
	binary.Write(&wav, binary.LittleEndian, uint32(24000))
	binary.Write(&wav, binary.LittleEndian, uint16(2))
	binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(48000))
	for i := 0; i < 24000; i++ {
		binary.Write(&wav, binary.LittleEndian, int16(2000*math.Sin(2*math.Pi*440*float64(i)/12000)))
	}
	save("voice.wav", wav.Bytes())
	video, err := os.ReadFile("../../scripts/tests/testdata/resources/sample.webm")
	if err != nil {
		t.Fatal(err)
	}
	save("sample.webm", video)
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 400 240] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	stream := "BT /F1 22 Tf 36 180 Td (Resource preview) Tj 0 -38 Td /F1 12 Tf (PDF original - separate download action) Tj ET"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	offsets := []int{}
	for index, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", xref)
	save("notes.pdf", pdf.Bytes())
	save("demo.html", []byte(`<!doctype html><html lang="zh-CN"><head><link rel="stylesheet" href="demo.css"></head><body><div class="eyebrow">INTERACTIVE DEMO</div><h2>每月存一点，看看会怎样</h2><p>调整金额，立即预览你的储蓄计划。</p><label>每月储蓄 <output id="amount">¥2,000</output><input id="budget" type="range" min="500" max="10000" step="500" value="2000"></label><div class="total"><span>一年后累计</span><strong id="total">¥24,000</strong><div class="bars"><i></i><i></i><i></i><i></i><i></i><i></i></div></div><button id="confirm">采用这个计划</button><span id="saved" role="status"></span><script src="demo.js"></script></body></html>`))
	save("demo.css", []byte(`*{box-sizing:border-box}body{margin:0;padding:22px;background:#f3f7f1;color:#294b3d;font:14px system-ui}.eyebrow{font-size:10px;letter-spacing:.15em;color:#64836c}h2{font-size:21px;margin:9px 0}p{font-size:12px;color:#6c7f70;margin:0 0 18px}label{display:block;font-size:12px}output{float:right;font-weight:600}input{width:100%;accent-color:#4c795f;margin:10px 0}.total{position:relative;border-radius:10px;background:#e3eddf;padding:13px 16px;margin:8px 0 14px;overflow:hidden}.total span{font-size:11px;display:block}.total strong{font-size:28px;display:block}.bars{position:absolute;right:16px;bottom:0;display:flex;gap:7px;align-items:end;height:74px}.bars i{width:15px;background:#a5bca0;height:20%}.bars i:nth-child(2){height:35%}.bars i:nth-child(3){height:50%}.bars i:nth-child(4){height:65%}.bars i:nth-child(5){height:80%}.bars i:nth-child(6){height:100%;background:#65895f}button{border:0;border-radius:7px;padding:9px 15px;color:white;background:#426f51;font:12px system-ui;cursor:pointer}#saved{font-size:11px;margin-left:10px}`))
	save("demo.js", []byte(`const input=document.querySelector('#budget');input.oninput=()=>{document.querySelector('#amount').textContent='¥'+Number(input.value).toLocaleString();document.querySelector('#total').textContent='¥'+(Number(input.value)*12).toLocaleString();};document.querySelector('#confirm').onclick=()=>{document.querySelector('#saved').textContent='已采用';window.pot.result({monthly:Number(input.value),annual:Number(input.value)*12});};try{parent.document.body.dataset.escape='yes';}catch{document.body.dataset.isolated='yes';}window.pot.resize(350);`))
	save("report.txt", []byte("Savings plan\nMonthly: 2000\nAnnual: 24000\n"))
}

func TestResourceHTTPToolsRangeDownloadAndIsolation(t *testing.T) {
	app, items := resourceApplication(t)
	handler := Handler(app, "operator")
	get := func(path, owner, rangeHeader string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", path+"?assistant_id="+owner, nil)
		request.Header.Set("Authorization", "Bearer operator")
		if rangeHeader != "" {
			request.Header.Set("Range", rangeHeader)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, resource := range items {
		response := get("/api/v1/resources/"+resource.ID+"/content", "main", "bytes=4-11")
		if response.Code != 206 || len(response.Body.Bytes()) != 8 || response.Header().Get("Accept-Ranges") != "bytes" {
			t.Fatal("range", resource.Name, response.Code, response.Header())
		}
		if get("/api/v1/resources/"+resource.ID, "other", "").Code < 400 {
			t.Fatal("cross owner read")
		}
		response = get("/api/v1/resources/"+resource.ID+"/download", "main", "")
		if response.Code != 200 || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("download", response.Code)
		}
		if resource.MIMEType == "text/html" {
			response = get("/api/v1/resources/"+resource.ID+"/render", "main", "")
			if response.Code != 200 || response.Header().Get("Content-Security-Policy") != resources.HTMLPolicy || !strings.Contains(response.Body.String(), "data:text/javascript;base64,") {
				t.Fatal("HTML sandbox", response.Code)
			}
			if resource.Download.MIMEType != "application/zip" {
				t.Fatal("bundle not downloadable")
			}
		}
	}
}

func TestResourceBrowserFixture(t *testing.T) {
	urlFile := os.Getenv("POT_RESOURCE_FIXTURE_URL_FILE")
	if urlFile == "" {
		t.Skip("opt-in local browser fixture")
	}
	app, _ := resourceApplication(t)
	upstream := httptest.NewServer(Handler(app, "operator"))
	defer upstream.Close()
	config := webui.NewConfig()
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	browser, err := webui.New(webui.Options{Origin: origin, Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "operator", Client: upstream.Client()}, Assets: webassets.FS()})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	login := httptest.NewRequest("POST", origin+"/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	logged := httptest.NewRecorder()
	browser.ServeHTTP(logged, login)
	cookie := logged.Result().Cookies()[0]
	done := make(chan struct{}, 1)
	repo, _ := filepath.Abs("../..")
	files := http.FileServer(http.Dir(repo))
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/stop" {
			select {
			case done <- struct{}{}:
			default:
			}
			return
		}
		r.AddCookie(cookie)
		if strings.HasPrefix(r.URL.Path, "/scripts/") || strings.HasPrefix(r.URL.Path, "/web/") {
			w.Header().Set("Content-Security-Policy", "frame-src 'self' blob:; object-src 'none'")
			files.ServeHTTP(w, r)
			return
		}
		browser.ServeHTTP(w, r)
	})
	server.Start()
	defer func() { server.CloseClientConnections(); server.Close() }()
	if err := os.WriteFile(urlFile, []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Fatal("resource browser fixture expired")
	}
	_ = app.Control.Close()
}
