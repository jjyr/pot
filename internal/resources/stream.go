package resources

import (
	"encoding/base64"
	"errors"
	"io"

	"github.com/jjyr/pot/pkg/protocol"
)

// Reader gives native media elements bounded seekable reads through the same
// captured local UI adapter as ordinary API calls, without buffering a whole movie.
type Reader struct {
	File        File
	ReadChunk   func(uint64) (protocol.FileChunk, error)
	position    int64
	buffer      []byte
	bufferStart int64
}

func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.position
	case io.SeekEnd:
		offset += int64(r.File.Size)
	default:
		return 0, errors.New("invalid resource seek")
	}
	if offset < 0 {
		return 0, errors.New("invalid resource seek")
	}
	r.position = offset
	return offset, nil
}
func (r *Reader) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	if r.position >= int64(r.File.Size) {
		return 0, io.EOF
	}
	if r.position >= r.bufferStart && r.position < r.bufferStart+int64(len(r.buffer)) {
		n := copy(out, r.buffer[r.position-r.bufferStart:])
		r.position += int64(n)
		return n, nil
	}
	chunk, err := r.ReadChunk(uint64(r.position))
	if err != nil {
		return 0, err
	}
	if chunk.Offset != uint64(r.position) || chunk.Size != r.File.Size || chunk.Revision != r.File.Digest || len(chunk.Data) > 4*((ChunkSize+2)/3) {
		return 0, errors.New("resource chunk identity changed")
	}
	bytes, err := base64.StdEncoding.Strict().DecodeString(chunk.Data)
	if err != nil || len(bytes) == 0 || len(bytes) > ChunkSize || uint64(r.position)+uint64(len(bytes)) > r.File.Size {
		return 0, errors.New("invalid resource chunk")
	}
	r.buffer = bytes
	r.bufferStart = r.position
	n := copy(out, bytes)
	r.position += int64(n)
	return n, nil
}
