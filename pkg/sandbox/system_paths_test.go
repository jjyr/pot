package sandbox

import (
	"slices"
	"testing"
)

func TestSystemReadPathsArePlatformSpecific(t *testing.T) {
	for _, tc := range []struct {
		goos    string
		allowed []string
		denied  []string
	}{
		{
			goos:    "darwin",
			allowed: []string{"/System", "/Applications", "/etc/ssl/cert.pem"},
			denied:  []string{"/etc/ssl/certs", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/fonts", "/etc/chromium", "/etc/chromium.d", "/var/cache/fontconfig"},
		},
		{
			goos:    "linux",
			allowed: []string{"/etc/ssl/certs", "/etc/ssl/cert.pem", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/fonts", "/etc/chromium"},
			denied:  []string{"/System", "/Applications", "/Library/Apple", "/Library/Fonts"},
		},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			paths, err := systemReadPaths(tc.goos)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range append(tc.allowed, "/usr", "/etc/hosts", "/etc/resolv.conf") {
				if !slices.Contains(paths, path) {
					t.Errorf("required path missing: %s", path)
				}
			}
			for _, path := range append(tc.denied, "/", "/etc", "/etc/ssl", "/run", "/home", "/Users") {
				if slices.Contains(paths, path) {
					t.Errorf("unnecessary path granted: %s", path)
				}
			}
		})
	}
}

func TestSystemReadPathsRejectUnsupportedPlatforms(t *testing.T) {
	for _, goos := range []string{"", "windows", "freebsd"} {
		if paths, err := systemReadPaths(goos); err == nil || len(paths) != 0 {
			t.Fatalf("unsupported platform %q received paths: %v, %v", goos, paths, err)
		}
	}
}
