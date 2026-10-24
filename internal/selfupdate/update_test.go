package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestMain(m *testing.M) {
	buildinfo.Version = "0.1.0"
	if len(os.Args) == 3 && os.Args[1] == "_update-worker" {
		if err := Worker(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "FIXTURE worker failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func mustWrite(t *testing.T, path string, value any) {
	t.Helper()
	if err := instance.WriteJSON(path, value); err != nil {
		t.Fatal(err)
	}
}
func signed(t *testing.T, r Release) ([]byte, string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(r)
	data, _ := json.Marshal(Envelope{base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	return data, base64.StdEncoding.EncodeToString(pub)
}
func TestSignedReleaseTrustAndFormat(t *testing.T) {
	r := Release{Version: "0.2.0", StorageFormat: StorageFormat, Artifacts: []Artifact{{"cli", "linux-arm64", releaseOrigin + "v0.2.0/pot.tar.gz", strings.Repeat("ab", 32), 123}}}
	data, key := signed(t, r)
	if _, err := Verify(data, key); err != nil {
		t.Fatal(err)
	}
	var e Envelope
	json.Unmarshal(data, &e)
	e.Payload = base64.StdEncoding.EncodeToString([]byte(`{"version":"9.9.9"}`))
	tampered, _ := json.Marshal(e)
	if _, err := Verify(tampered, key); err == nil {
		t.Fatal("accepted modified release")
	}
	for _, change := range []func(*Release){func(r *Release) { r.StorageFormat = "future" }, func(r *Release) { r.Artifacts[0].URL = "https://evil.example/pot" }, func(r *Release) { r.Artifacts[0].Size = 2 << 30 }, func(r *Release) { r.Artifacts = append(r.Artifacts, r.Artifacts[0]) }} {
		candidate := r
		candidate.Artifacts = append([]Artifact{}, r.Artifacts...)
		change(&candidate)
		data, key = signed(t, candidate)
		if _, err := Verify(data, key); err == nil {
			t.Fatal("accepted unsupported release", candidate)
		}
	}
	if !newer("0.10.0", "0.9.9") || newer("0.2.0", "0.2.0") || newer("0.1.0", "development") {
		t.Fatal("incorrect version ordering")
	}
}
func archiveBytes(t *testing.T, entries []*tar.Header, contents []string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	w := tar.NewWriter(gz)
	for i, h := range entries {
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := io.WriteString(w, contents[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	w.Close()
	gz.Close()
	return b.Bytes()
}
func TestArchiveConfinementAndRelocatableLinks(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "a.tar.gz")
			os.WriteFile(file, archiveBytes(t, []*tar.Header{{Name: name, Mode: 0644, Size: 1, Typeflag: tar.TypeReg}}, []string{"x"}), 0600)
			if err := extract(file, filepath.Join(dir, "out"), false); err == nil {
				t.Fatal("accepted traversal")
			}
		})
	}
	t.Run("node links", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "a.tar.gz")
		data := archiveBytes(t, []*tar.Header{{Name: "pot/Resources/node/lib/npm.js", Mode: 0755, Size: 1, Typeflag: tar.TypeReg}, {Name: "pot/Resources/node/bin/npm", Mode: 0777, Typeflag: tar.TypeSymlink, Linkname: "../lib/npm.js"}}, []string{"x", ""})
		os.WriteFile(file, data, 0600)
		if err := extract(file, filepath.Join(dir, "out"), false); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(filepath.Join(dir, "out/pot/Resources/node/bin/npm")); err != nil || string(data) != "x" {
			t.Fatal("broken relocated npm link", err)
		}
	})
	t.Run("link ancestor", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "a.tar.gz")
		data := archiveBytes(t, []*tar.Header{{Name: "pot/link", Mode: 0777, Typeflag: tar.TypeSymlink, Linkname: "."}, {Name: "pot/link/escape", Mode: 0644, Size: 1, Typeflag: tar.TypeReg}}, []string{"", "x"})
		os.WriteFile(file, data, 0600)
		if err := extract(file, filepath.Join(dir, "out"), false); err == nil {
			t.Fatal("accepted symlink ancestor")
		}
	})
}
func TestDownloadRejectsChecksumAndPreservesInstalledFiles(t *testing.T) {
	directory := t.TempDir()
	metadata, _ := json.Marshal(Installation{Version: "0.2.0", Kind: "cli", Platform: "linux-arm64", StorageFormat: StorageFormat})
	data := archiveBytes(t, []*tar.Header{{Name: "pot/Resources/update.json", Mode: 0644, Size: int64(len(metadata)), Typeflag: tar.TypeReg}}, []string{string(metadata)})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	sum := sha256.Sum256(data)
	a := Artifact{Kind: "cli", Platform: "linux-arm64", URL: server.URL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
	m := &Manager{Host: &Host{Directory: directory}, client: server.Client(), release: &Release{Version: "0.2.0"}}
	wrong := a
	wrong.SHA256 = strings.Repeat("00", 32)
	if err := m.download(context.Background(), wrong, "0.2.0"); err == nil {
		t.Fatal("accepted checksum mismatch")
	}
	if err := m.download(context.Background(), a, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "prepared.json")); err != nil {
		t.Fatal(err)
	}
}

// Both package generations use authenticated metadata, never a guessed basename.
func TestCLIPackageNamingCompatibility(t *testing.T) {
	if StorageFormat != "pot-go-2026-10-5" {
		t.Fatal("0.0.1 update compatibility changed")
	}
	for _, fixture := range []struct{ version, name, directory string }{
		{"0.0.1", "pot-go-0.0.1-linux-arm64.tar.gz", "pot-go-linux-arm64"},
		{"0.0.2", "pot-0.0.2-linux-arm64.tar.gz", "pot-linux-arm64"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			metadata, _ := json.Marshal(Installation{Version: fixture.version, Kind: "cli", Platform: "linux-arm64", StorageFormat: storageFormat()})
			data := archiveBytes(t, []*tar.Header{{Name: fixture.directory + "/Resources/update.json", Mode: 0644, Size: int64(len(metadata)), Typeflag: tar.TypeReg}}, []string{string(metadata)})
			sum := sha256.Sum256(data)
			artifact := Artifact{Kind: "cli", Platform: "linux-arm64", URL: releaseOrigin + "v" + fixture.version + "/" + fixture.name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
			envelope, key := signed(t, Release{Version: fixture.version, StorageFormat: storageFormat(), Artifacts: []Artifact{artifact}})
			if _, err := Verify(envelope, key); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
			defer server.Close()
			artifact.URL = server.URL
			m := &Manager{Host: &Host{Directory: t.TempDir()}, client: server.Client(), release: &Release{Version: fixture.version}}
			if err := m.download(context.Background(), artifact, fixture.version); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestConfirmationAndUnsupportedHost(t *testing.T) {
	m := New(context.Background(), nil, nil)
	status, _ := m.Status()
	if status.Supported {
		t.Fatal("source build is updateable")
	}
	directory := t.TempDir()
	mustWrite(t, filepath.Join(directory, "state.json"), State{Version: "0.2.0", Phase: "ready"})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Participant{Work: Work{Tasks: 2}})
	}))
	socketRoot, err := os.MkdirTemp("/tmp", "pot-u-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketRoot)
	socket := filepath.Join(socketRoot, "peer.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	mustWrite(t, filepath.Join(directory, "host-1.json"), Participant{Socket: socket})
	m = New(context.Background(), &Host{Directory: directory}, nil)
	body := json.RawMessage(`{"action":"install","version":"0.2.0","request_id":"attempt","interrupt":false}`)
	_, err = m.Call(context.Background(), "POST", body)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != "update_confirmation_required" {
		t.Fatal("did not require interruption confirmation", err)
	}
	if m.state().Phase != "ready" {
		t.Fatal("confirmation modified installation state")
	}
}

// Subprocess fixture uses the real host registration, shutdown handshake and
// exec restart. No installed product, user service or model API is touched.
func TestHostProcess(t *testing.T) {
	root := os.Getenv("POT_UPDATE_TEST_ROOT")
	if root == "" {
		return
	}
	executable, _ := os.Executable()
	var metadata Installation
	data, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "../Resources/update.json"))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(data, &metadata)
	buildinfo.Version = metadata.Version
	fmt.Fprintf(os.Stderr, "FIXTURE host boot pid=%d version=%s at=%s\n", os.Getpid(), metadata.Version, time.Now().UTC().Format(time.RFC3339Nano))
	buildinfo.UpdatePublicKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	h, err := OpenHost()
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Run(context.Background(), func(ctx context.Context) error {
		if err := h.Bind(root, func() (Work, error) {
			if metadata.Version == "0.1.0" && os.Getenv("POT_UPDATE_TEST_WORK") == "1" {
				return Work{Tasks: 1}, nil
			}
			return Work{}, nil
		}); err != nil {
			return err
		}
		if !(metadata.Version == "0.2.0" && os.Getenv("POT_UPDATE_TEST_FAIL") == "1") {
			h.Ready()
			fmt.Fprintf(os.Stderr, "FIXTURE host ready pid=%d version=%s at=%s\n", os.Getpid(), metadata.Version, time.Now().UTC().Format(time.RFC3339Nano))
		}
		<-ctx.Done()
		fmt.Fprintf(os.Stderr, "FIXTURE host run stopped pid=%d version=%s cleanup=%s at=%s\n", os.Getpid(), metadata.Version, os.Getenv("POT_UPDATE_TEST_CLEANUP"), time.Now().UTC().Format(time.RFC3339Nano))
		if os.Getenv("POT_UPDATE_TEST_CLEANUP") == "1" {
			return errors.New("cleanup unconfirmed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func copyTestBinary(t *testing.T, destination string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(destination, data, 0755); err != nil {
		t.Fatal(err)
	}
}

// Stop only the fixture-owned host, preserving Kill-then-Wait ordering.
// Cleanup failures remain test failures with bounded diagnostics.
func stopTestUpdateHost(t *testing.T, process *exec.Cmd, logPath string) {
	t.Helper()
	pid := process.Process.Pid
	snapshot := func(stage string) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		body, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "pid=,ppid=,state=,etime=,comm=").CombinedOutput()
		t.Logf("FIXTURE ps stage=%s owned_pid=%d result=%q error=%v", stage, pid, body, err)
	}
	logHost := func() {
		body, err := os.ReadFile(logPath)
		if len(body) > 16384 {
			body = body[len(body)-16384:]
		}
		t.Logf("FIXTURE synthetic host.log owned_pid=%d error=%v:\n%s", pid, err, body)
	}
	started := time.Now()
	killErr := process.Process.Kill()
	t.Logf("FIXTURE Kill owned_pid=%d returned=%T(%v) elapsed=%s", pid, killErr, killErr, time.Since(started))
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		t.Errorf("fixture Kill failed: %v", killErr)
	}
	waited := make(chan error, 1)
	go func() { waited <- process.Wait() }() // Exactly one Wait for this Cmd.
	select {
	case waitErr := <-waited:
		t.Logf("FIXTURE Wait owned_pid=%d returned=%T(%v) elapsed=%s state=%v", pid, waitErr, waitErr, time.Since(started), process.ProcessState)
		var exited *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exited) {
			t.Errorf("fixture Wait failed: %v", waitErr)
		}
		logHost()
		return
	case <-time.After(3 * time.Second):
		t.Errorf("fixture owned_pid=%d did not exit within 3s after Kill", pid)
	}
	snapshot("stalled-after-kill")
	quitErr := process.Process.Signal(syscall.SIGQUIT)
	t.Logf("FIXTURE diagnostic SIGQUIT owned_pid=%d returned=%T(%v)", pid, quitErr, quitErr)
	select {
	case waitErr := <-waited:
		t.Logf("FIXTURE Wait after SIGQUIT owned_pid=%d error=%v", pid, waitErr)
	case <-time.After(2 * time.Second):
		// Cleanup only the original Cmd's process, never arbitrary PIDs or groups.
		retryErr := process.Process.Kill()
		t.Logf("FIXTURE final cleanup Kill owned_pid=%d returned=%T(%v)", pid, retryErr, retryErr)
		select {
		case waitErr := <-waited:
			t.Logf("FIXTURE Wait after final Kill owned_pid=%d error=%v", pid, waitErr)
		case <-time.After(3 * time.Second):
			t.Errorf("fixture child remains unconfirmed after bounded cleanup: pid=%d", pid)
		}
	}
	logHost()
}

func TestWorkerRestartsWholeInstallationAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"success", "confirmation", "cleanup", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			install := filepath.Join(base, "pot")
			directory := install + ".updates"
			root := filepath.Join(base, "data")
			os.MkdirAll(root, 0700)
			os.WriteFile(filepath.Join(root, "identity.json"), []byte(`{"id":"original"}`), 0600)
			writePackage := func(target, version string) {
				copyTestBinary(t, filepath.Join(target, "bin/pot"))
				os.MkdirAll(filepath.Join(target, "Resources"), 0755)
				mustWrite(t, filepath.Join(target, "Resources/update.json"), Installation{Version: version, Kind: "cli", Platform: "darwin-arm64", StorageFormat: StorageFormat})
			}
			writePackage(install, "0.1.0")
			process := exec.Command(filepath.Join(install, "bin/pot"), "-test.run=^TestHostProcess$")
			process.Env = append(os.Environ(), "POT_UPDATE_TEST_ROOT="+root)
			if scenario == "confirmation" {
				process.Env = append(process.Env, "POT_UPDATE_TEST_WORK=1")
			}
			if scenario == "cleanup" {
				process.Env = append(process.Env, "POT_UPDATE_TEST_CLEANUP=1")
			}
			if scenario == "rollback" {
				process.Env = append(process.Env, "POT_UPDATE_TEST_FAIL=1")
			}
			log, err := os.Create(filepath.Join(base, "host.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			process.Stdout = log
			process.Stderr = log
			if err = process.Start(); err != nil {
				t.Fatal(err)
			}
			defer stopTestUpdateHost(t, process, log.Name())
			wait := func(predicate func() bool) {
				t.Helper()
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					if predicate() {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				for _, path := range []string{log.Name(), filepath.Join(directory, "state.json"), filepath.Join(directory, "worker.log")} {
					data, readErr := os.ReadFile(path)
					if len(data) > 16384 {
						data = data[len(data)-16384:]
					}
					t.Logf("FIXTURE timeout file=%s error=%v:\n%s", filepath.Base(path), readErr, data)
				}
				t.Fatal("fixture timeout")
			}
			var peers []Participant
			wait(func() bool { peers, _ = participants(directory); return len(peers) == 1 && peers[0].Ready })
			expectedPeers := 1
			if scenario == "success" {
				root2 := filepath.Join(base, "browser-data")
				os.MkdirAll(root2, 0700)
				browser := exec.Command(filepath.Join(install, "bin/pot"), "-test.run=^TestHostProcess$")
				browser.Env = append(os.Environ(), "POT_UPDATE_TEST_ROOT="+root2)
				browser.Stdout = log
				browser.Stderr = log
				if err = browser.Start(); err != nil {
					t.Fatal(err)
				}
				defer stopTestUpdateHost(t, browser, log.Name())
				expectedPeers = 2
				wait(func() bool {
					peers, _ = participants(directory)
					return len(peers) == 2 && peers[0].Ready && peers[1].Ready
				})
			}

			stage := filepath.Join(directory, "stage-0.2.0/pot")
			writePackage(stage, "0.2.0")
			mustWrite(t, filepath.Join(directory, "prepared.json"), struct{ Version, Path string }{"0.2.0", stage})
			mustWrite(t, filepath.Join(directory, "state.json"), State{Version: "0.2.0", Phase: "installing"})
			mustWrite(t, filepath.Join(directory, "job.json"), Job{Install: install, Directory: directory, Version: "0.2.0", Interrupt: scenario != "confirmation"})
			oldTimeout := healthTimeout
			healthTimeout = time.Second
			defer func() { healthTimeout = oldTimeout }()
			if scenario == "success" {
				binary, readErr := os.ReadFile(filepath.Join(stage, "bin/pot"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				metadata, readErr := os.ReadFile(filepath.Join(stage, "Resources/update.json"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				archive := archiveBytes(t, []*tar.Header{{Name: "pot/bin/pot", Mode: 0755, Size: int64(len(binary)), Typeflag: tar.TypeReg}, {Name: "pot/Resources/update.json", Mode: 0644, Size: int64(len(metadata)), Typeflag: tar.TypeReg}}, []string{string(binary), string(metadata)})
				sum := sha256.Sum256(archive)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive) }))
				defer server.Close()
				artifact := Artifact{Kind: "cli", Platform: "darwin-arm64", URL: server.URL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(archive))}
				mustWrite(t, filepath.Join(directory, "state.json"), State{Version: "0.2.0", Phase: "available"})
				manager := New(context.Background(), &Host{Install: install, Directory: directory, Package: Installation{Kind: "cli", Platform: "darwin-arm64"}}, nil)
				manager.release = &Release{Version: "0.2.0", Artifacts: []Artifact{artifact}}
				manager.client = server.Client()
				_, err = manager.Call(context.Background(), "POST", json.RawMessage(`{"action":"download","version":"0.2.0","request_id":"one-click","interrupt":false}`))
				if err != nil {
					t.Fatal(err)
				}
				// No frontend follows up the click. The backend must install,
				// restart both hosts and complete the transaction by itself.
				wait(func() bool { return manager.state().Phase == "current" })
			} else {
				err = Worker(filepath.Join(directory, "job.json"))
			}
			if (scenario == "cleanup" || scenario == "rollback") != (err != nil) {
				t.Fatal("unexpected update result", err)
			}
			expected := "0.2.0"
			if scenario != "success" {
				expected = "0.1.0"
			}
			wait(func() bool {
				peers, _ = participants(directory)
				if len(peers) != expectedPeers {
					return false
				}
				for _, peer := range peers {
					if peer.Version != expected {
						return false
					}
				}
				return true
			})
			data, _ := os.ReadFile(filepath.Join(root, "identity.json"))
			if string(data) != `{"id":"original"}` {
				t.Fatal("update changed machine identity")
			}
		})
	}
}

func TestProtectedDataRootsDisableReplacement(t *testing.T) {
	base := t.TempDir()
	h := &Host{Install: filepath.Join(base, "pot"), Directory: filepath.Join(base, "pot.updates")}
	h.Protect(filepath.Join(base, "user-data"))
	if h.unsupported {
		t.Fatal("separate user data was blocked")
	}
	h.Protect(filepath.Join(base, "pot", "data"))
	if !h.unsupported {
		t.Fatal("installation-contained user data could be replaced")
	}
}

func TestObsoletePreparedUpdateCannotDowngradeInstallation(t *testing.T) {
	directory := t.TempDir()
	m := New(context.Background(), &Host{Directory: directory}, nil)
	for _, version := range []string{"0.0.9", "0.1.0"} {
		mustWrite(t, filepath.Join(directory, "state.json"), State{Version: version, Phase: "ready"})
		status, err := m.Status()
		if err != nil || status.Version != buildinfo.Version || status.Phase != "current" {
			t.Fatal("obsolete update remained advertised", status, err)
		}
		body, _ := json.Marshal(Command{Action: "install", Version: version, RequestID: "new-attempt"})
		_, err = m.Call(context.Background(), "POST", body)
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != "update_stale" {
			t.Fatal("accepted obsolete prepared update", err)
		}
	}
}

func TestReleasePinsCentralizedMigrationFingerprint(t *testing.T) {
	previous := buildinfo.UpdateSchemaDigest
	defer func() { buildinfo.UpdateSchemaDigest = previous }()
	buildinfo.UpdateSchemaDigest = strings.Repeat("ab", 32)
	release := Release{Version: "0.2.0", StorageFormat: storageFormat()}
	data, key := signed(t, release)
	if _, err := Verify(data, key); err != nil {
		t.Fatal(err)
	}
	buildinfo.UpdateSchemaDigest = strings.Repeat("cd", 32)
	if _, err := Verify(data, key); err == nil {
		t.Fatal("allowed migration-changing package rollback")
	}
}
