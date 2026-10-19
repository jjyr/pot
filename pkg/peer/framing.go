package peer

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

type signed struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing peer JSON")
	}
	return nil
}
func seal(key ed25519.PrivateKey, domain string, value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	signature := ed25519.Sign(key, append([]byte(domain+"\x00"), raw...))
	return json.Marshal(signed{raw, base64.RawURLEncoding.EncodeToString(signature)})
}
func open(raw []byte, identity Identity, domain string, out any) error {
	var value signed
	if decode(raw, &value) != nil || identity.Validate() != nil {
		return ErrUnauthorized
	}
	key, _ := base64.RawURLEncoding.DecodeString(identity.PublicKey)
	signature, err := base64.RawURLEncoding.DecodeString(value.Signature)
	if err != nil || !ed25519.Verify(key, append([]byte(domain+"\x00"), value.Payload...), signature) {
		return ErrUnauthorized
	}
	return decode(value.Payload, out)
}
func writePacket(w io.Writer, raw []byte, limit int) error {
	if len(raw) < 1 || len(raw) > limit {
		return fmt.Errorf("peer frame exceeds limit")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, raw)
}
func writeAll(w io.Writer, raw []byte) error {
	for len(raw) > 0 {
		n, err := w.Write(raw)
		if err != nil {
			return err
		}
		if n < 1 {
			return io.ErrShortWrite
		}
		raw = raw[n:]
	}
	return nil
}
func readPacket(r io.Reader, limit int) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > uint32(limit) {
		return nil, fmt.Errorf("peer frame exceeds limit")
	}
	raw := make([]byte, int(size))
	_, err := io.ReadFull(r, raw)
	return raw, err
}
