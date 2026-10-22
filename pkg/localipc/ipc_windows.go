package localipc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/Microsoft/go-winio"
	"github.com/jjyr/pot/pkg/privatefs"
	"net"
	"path/filepath"
	"strings"
)

func pipeName(path string) string {
	p, _ := filepath.Abs(path)
	return fmt.Sprintf(`\\.\pipe\pot-%x`, sha256.Sum256([]byte(strings.ToLower(p))))
}
func Listen(path string) (net.Listener, error) {
	sd, e := privatefs.SecurityDescriptor()
	if e != nil {
		return nil, e
	}
	return winio.ListenPipe(pipeName(path), &winio.PipeConfig{SecurityDescriptor: sd, InputBufferSize: 65536, OutputBufferSize: 65536})
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName(path))
}
