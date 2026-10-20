package peer

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

const peerALPN = "pot-peer/4"

// Each connection gets a fresh, in-memory certificate for the existing identity
// key. Pinning the public key instead of the certificate permits renewal without
// changing invitations or saved bindings. No certificate files or CA are needed.
func tlsConfig(identity Identity, key ed25519.PrivateKey, expected *Identity, server bool) (*tls.Config, error) {
	if validLocal(identity, key) != nil || expected != nil && expected.Validate() != nil {
		return nil, ErrUnauthorized
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: identity.NodeID},
		NotBefore:    now.Add(-5 * time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	private := append(ed25519.PrivateKey(nil), key...)
	der, err := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if err != nil {
		return nil, err
	}
	var pin Identity
	if expected != nil {
		pin = *expected
	}
	config := &tls.Config{
		MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
		NextProtos:   []string{peerALPN},
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}},
		// Every connection must prove possession of both identity keys afresh.
		SessionTicketsDisabled: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			remote, err := certificateIdentity(state, server)
			if err != nil || expected != nil && remote != pin {
				return ErrUnauthorized
			}
			return nil
		},
	}
	if server {
		// Possession of a client key is required even for discovery and pairing.
		// Service admission separately decides whether that key is authorized.
		config.ClientAuth = tls.RequireAnyClientCert
	} else {
		// Replace CA/DNS trust with the mandatory verifier above. TLS still
		// verifies CertificateVerify and Finished; copying a pin/certificate
		// without its private key cannot authenticate a peer.
		config.InsecureSkipVerify = true
	}
	return config, nil
}

func certificateIdentity(state tls.ConnectionState, clientCertificate bool) (Identity, error) {
	if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != peerALPN || state.DidResume || len(state.PeerCertificates) != 1 {
		return Identity{}, ErrUnauthorized
	}
	cert := state.PeerCertificates[0]
	public, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return Identity{}, ErrUnauthorized
	}
	identity := NewIdentity(cert.Subject.CommonName, public)
	now := time.Now()
	if identity.Validate() != nil || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) || cert.IsCA || cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 || cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) != nil {
		return Identity{}, ErrUnauthorized
	}
	usage := x509.ExtKeyUsageServerAuth
	if clientCertificate {
		usage = x509.ExtKeyUsageClientAuth
	}
	for _, allowed := range cert.ExtKeyUsage {
		if allowed == usage {
			return identity, nil
		}
	}
	return Identity{}, ErrUnauthorized
}
