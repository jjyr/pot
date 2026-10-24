package control

import (
	"context"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestMachineInterfaceAddresses(t *testing.T) {
	t.Parallel()
	var values []net.Addr
	for _, text := range []string{"127.0.0.1/8", "::1/128", "169.254.1.1/16", "fe80::1/64", "224.0.0.1/4", "0.0.0.0/32", "fd00::2/64", "10.0.0.9/24", "192.168.1.2/24", "10.0.0.9/24", "::ffff:10.0.0.9/128"} {
		ip, network, err := net.ParseCIDR(text)
		if err != nil {
			t.Fatal(err)
		}
		network.IP = ip
		values = append(values, network)
	}
	want := []string{"10.0.0.9", "192.168.1.2", "fd00::2"}
	if got := machineIPAddresses(values); !reflect.DeepEqual(got, want) {
		t.Fatalf("interface addresses = %v, want %v", got, want)
	}
	if got := machineIPAddresses(nil); got == nil || len(got) != 0 {
		t.Fatalf("missing interfaces must produce an empty list, got %v", got)
	}
}

func TestMachineInventoryUsesHostnameWithoutChangingIdentity(t *testing.T) {
	t.Parallel()
	service := agentControl(t, "opaque-node-identity").service
	items, err := service.MachineList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0]["id"] != "opaque-node-identity" || items[0]["runtime_id"] != "opaque-node-identity" {
		t.Fatalf("machine routing identity changed: %v", items)
	}
	hostname, _ := os.Hostname()
	if hostname = strings.TrimSpace(hostname); hostname != "" && (items[0]["name"] != hostname || items[0]["hostname"] != hostname) {
		t.Fatalf("machine did not use current OS hostname: %v", items[0])
	}
	if items[0]["name"] == "opaque-node-identity" || items[0]["name"] == "" {
		t.Fatalf("machine has no recognizable name: %v", items[0])
	}
	if _, ok := items[0]["ip_addresses"].([]string); !ok {
		t.Fatalf("machine IPs must be an array: %v", items[0])
	}
}
