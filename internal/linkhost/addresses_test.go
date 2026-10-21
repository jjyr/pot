package linkhost

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveredInterfacesIncludePublicAndExcludeUnusableAddresses(t *testing.T) {
	devices := []net.Interface{{Name: "en0", Flags: net.FlagUp}, {Name: "down"}, {Name: "utun", Flags: net.FlagUp}, {Name: "lo0", Flags: net.FlagUp | net.FlagLoopback}}
	got := InterfaceAddresses("127.0.0.1:8793", devices, func(device net.Interface) ([]net.Addr, error) {
		var result []net.Addr
		for _, value := range map[string][]string{"en0": {"192.168.1.4/24", "192.168.1.4/24", "203.0.113.10/24", "2001:db8::10/64", "fe80::1/64", "169.254.1.1/16", "224.0.0.1/24", "fd00::4/64"}, "down": {"10.0.0.2/24"}, "utun": {"100.88.0.4/32", "fd7a:115c:a1e0::4/64"}, "lo0": {"127.0.0.1/8"}}[device.Name] {
			_, prefix, _ := net.ParseCIDR(value)
			prefix.IP = net.ParseIP(strings.Split(value, "/")[0])
			result = append(result, prefix)
		}
		return result, nil
	})
	want := map[string]string{"tcp://127.0.0.1:8793": "loopback", "tcp://192.168.1.4:8793": "private", "tcp://[fd00::4]:8793": "private", "tcp://100.88.0.4:8793": "tailscale", "tcp://[fd7a:115c:a1e0::4]:8793": "tailscale", "tcp://203.0.113.10:8793": "public", "tcp://[2001:db8::10]:8793": "public"}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for _, item := range got {
		if want[item.Endpoint] != item.Kind {
			t.Fatal(item)
		}
	}
}

func TestTailscaleDiscoveryNeverChangesServe(t *testing.T) {
	for _, test := range []struct {
		config   string
		setup    bool
		endpoint string
	}{
		{`{"TCP":{}}`, true, "tcp://codex.tail.ts.net:8793"},
		{`{"TCP":{"443":{"TCPForward":"127.0.0.1:8793"}}}`, false, "tcp://codex.tail.ts.net:443"},
	} {
		binary := filepath.Join(t.TempDir(), "tailscale")
		script := `#!/bin/sh
if [ "$1" = status ]; then echo '{"BackendState":"Running","Self":{"DNSName":"codex.tail.ts.net."}}'; exit 0; fi
if [ "$1" = serve ] && [ "$2" = status ]; then cat "$0.config"; exit 0; fi
echo mutation > "$0.mutated"
exit 1
`
		os.WriteFile(binary, []byte(script), 0700)
		os.WriteFile(binary+".config", []byte(test.config), 0600)
		got, err := DiscoverTailscaleAddress(context.Background(), binary, "127.0.0.1:8793")
		if err != nil || got.Endpoint != test.endpoint || got.RequiresSetup != test.setup {
			t.Fatal(got, err)
		}
		if _, err = os.Stat(binary + ".mutated"); !os.IsNotExist(err) {
			t.Fatal("discovery modified Tailscale")
		}
	}
}
