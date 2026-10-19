// Command sign-release provisions and uses the Ed25519 update signing key.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 2 && args[0] == "keygen" {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		der, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(args[1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = file.Write(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		err = errors.Join(err, file.Close())
		if err != nil {
			return err
		}
		fmt.Println(base64.StdEncoding.EncodeToString(public))
		return nil
	}
	if len(args) != 1 || args[0] != "sign" {
		return errors.New("usage: sign-release keygen PRIVATE_KEY_PATH | sign")
	}
	block, _ := pem.Decode([]byte(os.Getenv("POT_UPDATE_PRIVATE_KEY")))
	if block == nil {
		return errors.New("release private key is not PKCS8 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return errors.New("invalid release private key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return errors.New("release key must be Ed25519")
	}
	public, err := base64.StdEncoding.DecodeString(os.Getenv("POT_UPDATE_PUBLIC_KEY"))
	if err != nil || !key.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(public)) {
		return errors.New("release signing key does not match pinned public key")
	}
	payload, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(payload) > 1<<20 {
		return errors.New("release manifest exceeds limit")
	}
	fmt.Println(base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)))
	return nil
}
