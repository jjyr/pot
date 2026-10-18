package sandbox

import "fmt"

// Select policy before checking which paths exist. A foreign platform's path
// must not gain authority merely because a directory with that name exists.
func systemReadPaths(goos string) ([]string, error) {
	paths := []string{
		"/usr", "/bin", "/sbin", "/dev", "/opt",
		"/etc/hosts", "/etc/resolv.conf", "/etc/services", "/etc/localtime",
	}
	switch goos {
	case "darwin":
		return append(paths,
			"/System", "/Library/Apple", "/Library/Fonts", "/Applications",
			"/etc/ssl/cert.pem",
		), nil
	case "linux":
		return append(paths,
			"/etc/ssl/certs", "/etc/ssl/cert.pem", "/etc/ssl/certs/ca-certificates.crt",
			"/etc/ssl/ca-bundle.pem", "/etc/pki/tls/certs/ca-bundle.crt",
			"/etc/pki/tls/cacert.pem", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
			"/etc/fonts", "/etc/chromium", "/etc/chromium.d", "/var/cache/fontconfig",
		), nil
	default:
		return nil, fmt.Errorf("unsupported sandbox platform: %s", goos)
	}
}
