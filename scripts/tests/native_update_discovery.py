"""Isolate native update test builds without adding a product injection API."""

from contextlib import contextmanager
import http.server
import json
import os
import shlex
import shutil
import threading


TRANSPORT = """package selfupdate

import (
    "fmt"
    "net/http"
)

type nativeFixtureTransport struct{}

var nativeFixtureBase = func() *http.Transport {
    transport := http.DefaultTransport.(*http.Transport).Clone()
    transport.Proxy = nil
    return transport
}()

func (nativeFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
    path := request.URL.Path
    if request.Method != "GET" || request.URL.Scheme != "https" ||
        request.URL.Host != "github.com" || request.URL.RawQuery != "" ||
        request.URL.Fragment != "" || request.URL.RawPath != "" || request.URL.User != nil ||
        (path != "/jjyr/pot/releases/latest/download/update.json" &&
            path != "/jjyr/pot/releases/download/v0.2.0/target.zip") {
        return nil, fmt.Errorf("native update fixture refuses URL: %s", request.URL)
    }
    clone := request.Clone(request.Context())
    address := *request.URL
    clone.URL = &address
    clone.URL.Scheme = "http"
    clone.URL.Host = FIXTURE_ADDRESS
    return nativeFixtureBase.RoundTrip(clone)
}
"""


@contextmanager
def fixture_discovery(repo, directory):
    """Serve only the signed fixture feed/package; overlay only test builds."""
    flags = shlex.split(os.environ.get("GOFLAGS", ""))
    if any(flag == "-overlay" or flag.startswith("-overlay=") for flag in flags):
        raise ValueError("native update fixture requires its own isolated Go overlay")
    paths = {
        "/jjyr/pot/releases/latest/download/update.json": directory / "update.json",
        "/jjyr/pot/releases/download/v0.2.0/target.zip": directory / "target.zip",
    }
    requests = []

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            path = paths.get(self.path)
            if path is None or not path.is_file():
                self.send_error(404)
                return
            requests.append(self.path)
            with path.open("rb") as source:
                self.send_response(200)
                self.send_header("Content-Length", str(path.stat().st_size))
                self.end_headers()
                shutil.copyfileobj(source, self.wfile)

        def log_message(self, *_):
            pass

    with http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
        host, port = server.server_address
        transport = directory / "native_fixture_transport.go"
        transport.write_text(TRANSPORT.replace("FIXTURE_ADDRESS", json.dumps(f"{host}:{port}")))
        release = repo / "internal/selfupdate/release.go"
        original = release.read_text()
        needle = "return &http.Client{Timeout: 10 * time.Minute, CheckRedirect:"
        if original.count(needle) != 1:
            raise ValueError("native update fixture client overlay no longer matches source")
        replacement = directory / "native_fixture_release.go"
        replacement.write_text(
            original.replace(
                needle,
                "return &http.Client{Transport: nativeFixtureTransport{}, "
                "Timeout: 10 * time.Minute, CheckRedirect:",
            )
        )
        overlay = directory / "native_fixture_overlay.json"
        overlay.write_text(
            json.dumps(
                {
                    "Replace": {
                        str(release): str(replacement),
                        str(repo / "internal/selfupdate/native_fixture_transport.go"): str(
                            transport
                        ),
                    }
                }
            )
        )
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            yield shlex.join([*flags, f"-overlay={overlay}"])
            if not requests:
                raise AssertionError("native update fixture never received a discovery request")
        finally:
            server.shutdown()
            thread.join()
