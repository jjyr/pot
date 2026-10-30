package rollout

import (
	"io"

	"github.com/klauspost/compress/zstd"
)

const archiveWindowSize = 8 << 20

// Archive compression stays single-threaded with a bounded window. SpeedDefault
// is zstd's medium compression level; every frame includes a content checksum.
func newArchiveWriter(w io.Writer) (*zstd.Encoder, error) {
	return zstd.NewWriter(w,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(true),
		zstd.WithWindowSize(archiveWindowSize),
	)
}

func newArchiveReader(r io.Reader) (*zstd.Decoder, error) {
	return zstd.NewReader(r,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(archiveWindowSize),
		zstd.WithDecoderMaxMemory(archiveWindowSize),
		zstd.WithDecoderLowmem(true),
	)
}
