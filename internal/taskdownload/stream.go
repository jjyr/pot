// Package taskdownload validates and streams revision-bound Task artifacts.
package taskdownload

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/jjyr/pot/internal/protocol"
	"io"
)

const Limit uint64 = 1 << 30
const ChunkSize uint64 = 192 << 10

type Read func(context.Context, uint64, string) (protocol.FileChunk, error)

func Validate(c protocol.FileChunk, offset uint64, revision string, size uint64) ([]byte, error) {
	if c.Size > Limit || c.Offset != offset || offset > c.Size || c.Revision == "" || len(c.Revision) > 128 || (revision != "" && (revision != c.Revision || size != c.Size)) || len(c.Data) > int((ChunkSize+2)/3*4) {
		return nil, errors.New("invalid Task file chunk")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(c.Data)
	if err != nil || uint64(len(data)) > ChunkSize || uint64(len(data)) > c.Size-offset || (offset < c.Size && len(data) == 0) {
		return nil, errors.New("invalid Task file chunk data")
	}
	return data, nil
}

// Copy writes at most one chunk at a time. Callers publish a completed native
// temporary file atomically, or abort an HTTP response on any revision failure.
func Copy(ctx context.Context, w io.Writer, first protocol.FileChunk, read Read) error {
	if _, err := Validate(first, 0, "", 0); err != nil {
		return err
	}
	current := first
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := Validate(current, current.Offset, first.Revision, first.Size)
		if err != nil {
			return err
		}
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n != len(data) {
			return io.ErrShortWrite
		}
		offset := current.Offset + uint64(n)
		if offset == first.Size {
			return nil
		}
		current, err = read(ctx, offset, first.Revision)
		if err != nil {
			return err
		}
		if current.Offset != offset {
			return errors.New("Task file offset changed")
		}
	}
}
