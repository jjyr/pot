package controlplane

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
)

// ClientForCA augments system roots. Certificate verification and hostname
// checking always remain enabled; the PEM is safe to persist with membership.
func ClientForCA(base *http.Client, pem string) (*http.Client, error) {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	if pem == "" {
		return &client, nil
	}
	if len(pem) > 1024*1024 {
		return nil, fmt.Errorf("private CA PEM exceeds limit")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if base.Transport != nil {
		original, ok := base.Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("custom CA requires HTTP transport")
		}
		transport = original.Clone()
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM([]byte(pem)) {
		return nil, fmt.Errorf("private CA PEM has no certificate")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	if transport.TLSClientConfig != nil {
		config = transport.TLSClientConfig.Clone()
		config.RootCAs = roots
		config.InsecureSkipVerify = false
		if config.MinVersion < tls.VersionTLS12 {
			config.MinVersion = tls.VersionTLS12
		}
	}
	transport.TLSClientConfig = config
	client.Transport = transport
	return &client, nil
}
