// Package tailnet owns the optional userspace Tailscale node and tailnet-only
// HTTPS publication. Provider credentials and Control enrollment stay separate.
package tailnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"tailscale.com/tsnet"
	"time"
)

type Node struct {
	mu             sync.Mutex
	root, hostname string
	node           *tsnet.Server
	web            *http.Server
	origin         string
}

func New(root, hostname string) *Node { return &Node{root: root, hostname: hostname} }
func (n *Node) Connect(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.node != nil {
		return nil
	}
	dir := filepath.Join(n.root, "tailscale")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	node := &tsnet.Server{Dir: dir, Hostname: n.hostname, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
	if err := node.Start(); err != nil {
		node.Close()
		return err
	}
	n.node = node
	return nil
}
func (n *Node) Status(ctx context.Context) (any, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.node == nil {
		return map[string]any{"available": true, "state": "Disconnected"}, nil
	}
	client, err := n.node.LocalClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := client.Status(ctx)
	if err != nil {
		return nil, err
	}
	dns := ""
	if status.Self != nil {
		dns = status.Self.DNSName
	}
	return map[string]any{"available": true, "state": status.BackendState, "auth_url": status.AuthURL, "dns_name": dns, "tailnet": status.CurrentTailnet, "ips": status.TailscaleIPs}, nil
}
func (n *Node) SetWeb(ctx context.Context, port int, enabled bool) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !enabled {
		if n.web != nil {
			n.web.Close()
			n.web = nil
		}
		n.origin = ""
		return "", nil
	}
	if n.node == nil {
		return "", errors.New("connect Tailscale first")
	}
	if port <= 0 || port > 65535 {
		return "", errors.New("invalid browser loopback port")
	}
	client, err := n.node.LocalClient()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	status, err := client.Status(ctx)
	if err != nil {
		return "", err
	}
	if status.BackendState != "Running" || status.Self == nil {
		return "", errors.New("complete Tailscale login and device approval first")
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if dns == "" {
		return "", errors.New("enable MagicDNS in Tailscale DNS settings")
	}
	if _, _, err = client.CertPair(ctx, dns); err != nil {
		return "", fmt.Errorf("enable HTTPS certificates in Tailscale DNS settings: %w", err)
	}
	origin := "https://" + dns
	if n.web != nil && n.origin == origin {
		return origin, nil
	}
	listener, err := n.node.ListenTLS("tcp", ":443")
	if err != nil {
		return "", err
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", fmt.Sprint(port))}
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	if n.web != nil {
		n.web.Close()
	}
	n.web = server
	n.origin = origin
	go server.Serve(listener)
	return origin, nil
}
func (n *Node) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	var err error
	if n.web != nil {
		err = n.web.Close()
		n.web = nil
	}
	if n.node != nil {
		err = errors.Join(err, n.node.Close())
		n.node = nil
	}
	n.origin = ""
	return err
}
