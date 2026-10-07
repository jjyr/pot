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
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
	"unsafe"
)

var potWeb sync.Map

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
	return potResult(map[string]any{"state": status.BackendState, "auth_url": status.AuthURL, "dns_name": dns, "tailnet": status.CurrentTailnet}, nil)
}

//export PotTsEnable
func PotTsEnable(id C.int, port C.int) *C.char {
	s := getServer(id)
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
	if status.BackendState != "Running" || status.Self == nil || status.Self.DNSName == "" {
		return potResult(nil, fmt.Errorf("Complete Tailscale login and device approval first"))
	}
	dns := status.Self.DNSName
	if dns[len(dns)-1] == '.' {
		dns = dns[:len(dns)-1]
	}
	// Verify certificate provisioning before declaring HTTPS ready. GetCertificate renews on demand.
	if _, _, err = lc.CertPair(ctx, dns); err != nil {
		return potResult(nil, fmt.Errorf("Enable MagicDNS and HTTPS certificates in Tailscale DNS settings: %w", err))
	}
	if _, ok := potWeb.Load(id); !ok {
		ln, err := s.s.ListenTLS("tcp", ":443")
		if err != nil {
			return potResult(nil, err)
		}
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", int(port)))
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorLog = nil
		web := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
		potWeb.Store(id, web)
		go web.Serve(ln)
	}
	return potResult(map[string]any{"origin": "https://" + dns}, nil)
}

//export PotTsDisconnect
func PotTsDisconnect(id C.int) *C.char {
	if value, ok := potWeb.LoadAndDelete(id); ok {
		value.(*http.Server).Close()
	}
	if TsnetClose(id) != 0 {
		return potResult(nil, fmt.Errorf("Tailscale shutdown failed"))
	}
	return potResult(map[string]any{"ok": true}, nil)
}

//export PotTsFree
func PotTsFree(value *C.char) { C.free(unsafe.Pointer(value)) }
