package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Only an explicit local Configure action reaches here. Existing unrelated
// Serve settings are never replaced; Funnel and ACLs are never enabled.
func configureHostTailscale(ctx context.Context) (string, error) {
	binary, err := exec.LookPath("tailscale")
	if err != nil {
		return "", errors.New("tailscale_not_installed")
	}
	return configureHostTailscaleBinary(ctx, binary)
}
func configureHostTailscaleBinary(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, binary, "status", "--json").Output()
	if err != nil {
		return "", errors.New("tailscale_status_failed")
	}
	var status struct {
		BackendState string
		Self         *struct{ DNSName string }
	}
	if json.Unmarshal(raw, &status) != nil || status.BackendState != "Running" || status.Self == nil {
		return "", errors.New("tailscale_login_required")
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if dns == "" {
		return "", errors.New("tailscale_magicdns_required")
	}
	read := func() (map[string]json.RawMessage, error) {
		raw, e := exec.CommandContext(ctx, binary, "serve", "status", "--json").Output()
		if e != nil {
			return nil, e
		}
		var v map[string]json.RawMessage
		e = json.Unmarshal(raw, &v)
		return v, e
	}
	config, err := read()
	if err != nil {
		return "", errors.New("tailscale_serve_status_failed")
	}
	existing := false
	for _, v := range config {
		if string(v) != "null" && string(v) != "{}" && string(v) != "[]" && string(v) != "false" && string(v) != "\"\"" {
			existing = true
		}
	}
	matches := func(config map[string]json.RawMessage) bool {
		var web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
		if json.Unmarshal(config["Web"], &web) != nil {
			return false
		}
		site, ok := web[dns+":443"]
		return ok && site.Handlers["/"].Proxy == "http://"+hostListen
	}
	if matches(config) {
		return "https://" + dns, nil
	}
	if existing {
		return "", errors.New("tailscale_serve_in_use_keep_existing_configuration")
	}
	if err = exec.CommandContext(ctx, binary, "serve", "--bg", "http://"+hostListen).Run(); err != nil {
		return "", errors.New("tailscale_serve_failed_check_https_settings")
	}
	config, err = read()
	if err != nil || !matches(config) {
		return "", errors.New("tailscale_serve_endpoint_not_confirmed")
	}
	return "https://" + dns, nil
}
