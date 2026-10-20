package peer

import (
	"encoding/binary"
	"fmt"
	"io"
)

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
