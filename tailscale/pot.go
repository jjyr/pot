// Pot's libtailscale adapter. TLS terminates in this process, only on the tailnet.
package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
	"unsafe"
)

var potWeb sync.Map

type potListener struct {
	server *http.Server
	https  bool
}

var potWebMu sync.Mutex

func potResult(value any, err error) *C.char {
	if err != nil {
		value = map[string]any{"error": err.Error()}
	}
	b, _ := json.Marshal(value)
	return C.CString(string(b))
}

//export PotTsConnect
func PotTsConnect(dir, hostname *C.char) *C.char {
	id := TsnetNewServer()
	s := getServer(id)
	s.s.Dir = C.GoString(dir)
	s.s.Hostname = C.GoString(hostname)
	s.s.Logf = func(string, ...any) {}
	s.s.UserLogf = s.s.Logf
	if TsnetStart(id) != 0 {
		err := fmt.Errorf("%s", s.lastErr)
		TsnetClose(id)
		return potResult(nil, err)
	}
	return potResult(map[string]any{"id": int(id)}, nil)
}

//export PotTsStatus
func PotTsStatus(id C.int) *C.char {
	s := getServer(id)
	if s == nil {
		return potResult(nil, fmt.Errorf("Tailscale is disconnected"))
	}
	lc, err := s.s.LocalClient()
	if err != nil {
		return potResult(nil, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := lc.Status(ctx)
	if err != nil {
		return potResult(nil, err)
	}
	dns := ""
	if status.Self != nil {
		dns = status.Self.DNSName
	}
	return potResult(map[string]any{"state": status.BackendState, "auth_url": status.AuthURL, "dns_name": dns, "tailnet": status.CurrentTailnet, "ips": status.TailscaleIPs}, nil)
}

//export PotTsEnable
func PotTsEnable(id C.int, port C.int) *C.char {
	return PotTsSetWeb(id, port, 1, 1)
}

//export PotTsSetWeb
func PotTsSetWeb(id C.int, port C.int, enabled C.int, secure C.int) *C.char {
	potWebMu.Lock()
	defer potWebMu.Unlock()
	s := getServer(id)
	if enabled == 0 {
		if value, ok := potWeb.LoadAndDelete(id); ok {
			value.(*potListener).server.Close()
		}
		return potResult(map[string]any{"origin": "http://localhost"}, nil)
	}
	if s == nil {
		return potResult(nil, fmt.Errorf("Connect Tailscale first"))
	}
	lc, err := s.s.LocalClient()
	if err != nil {
		return potResult(nil, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := lc.Status(ctx)
	if err != nil {
		return potResult(nil, err)
	}
	if status.BackendState != "Running" || status.Self == nil {
		return potResult(nil, fmt.Errorf("Complete Tailscale login and device approval first"))
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	https := secure != 0
	host := dns
	if host == "" && len(status.TailscaleIPs) > 0 {
		host = status.TailscaleIPs[0].String()
	}
	if host == "" {
		return potResult(nil, fmt.Errorf("Tailscale address unavailable"))
	}
	scheme, bind := "http", ":80"
	if https {
		if dns == "" {
			return potResult(nil, fmt.Errorf("Enable MagicDNS in Tailscale DNS settings"))
		}
		if _, _, err = lc.CertPair(ctx, dns); err != nil {
			return potResult(nil, fmt.Errorf("Enable HTTPS certificates in Tailscale DNS settings: %w", err))
		}
		scheme, bind = "https", ":443"
	}
	origin := scheme + "://" + host
	if value, ok := potWeb.Load(id); ok {
		previous := value.(*potListener)
		if previous.https == https {
			return potResult(map[string]any{"origin": origin}, nil)
		}
	}
	var ln net.Listener
	if https {
		ln, err = s.s.ListenTLS("tcp", bind)
	} else {
		ln, err = s.s.Listen("tcp", bind)
	}
	if err != nil {
		return potResult(nil, err)
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", int(port)))
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorLog = nil
	web := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	if value, ok := potWeb.LoadAndDelete(id); ok {
		value.(*potListener).server.Close()
	}
	potWeb.Store(id, &potListener{server: web, https: https})
	go web.Serve(ln)
	return potResult(map[string]any{"origin": origin}, nil)
}

//export PotTsDisconnect
func PotTsDisconnect(id C.int) *C.char {
	potWebMu.Lock()
	defer potWebMu.Unlock()
	if value, ok := potWeb.LoadAndDelete(id); ok {
		value.(*potListener).server.Close()
	}
	if TsnetClose(id) != 0 {
		return potResult(nil, fmt.Errorf("Tailscale shutdown failed"))
	}
	return potResult(map[string]any{"ok": true}, nil)
}

//export PotTsFree
func PotTsFree(value *C.char) { C.free(unsafe.Pointer(value)) }
