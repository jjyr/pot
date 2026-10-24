package control

import (
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

func TestLocalHostMetadata(t *testing.T) {
	t.Parallel()
	service := agentControl(t, "opaque-node-identity").service
	host := service.LocalHost()
	hostname, _ := os.Hostname()
	if host["hostname"] != strings.TrimSpace(hostname) {
		t.Fatalf("host did not use current OS hostname: %v", host)
	}
	if _, ok := host["ip_addresses"].([]string); !ok {
		t.Fatalf("host IPs must be an array: %v", host)
	}
	for _, key := range []string{"id", "runtime_id", "policy"} {
		if _, ok := host[key]; ok {
			t.Fatalf("host metadata must not duplicate node identity or permissions: %v", host)
		}
	}
}
