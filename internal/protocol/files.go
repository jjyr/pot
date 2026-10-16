package protocol

import (
	"encoding/hex"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"net/url"
	"strings"
)

func ValidFilePath(value string) bool { return len(value) <= 4096 && core.SafeRelativePath(value) }
func ParseFileLink(link string) (string, string, error) {
	rest, ok := strings.CutPrefix(link, "pot-file://")
	if !ok {
		return "", "", fmt.Errorf("invalid file link")
	}
	task, encoded, ok := strings.Cut(rest, "/")
	if !ok || !validUUID(task) || strings.ContainsAny(encoded, "?#\\") {
		return "", "", fmt.Errorf("invalid file link")
	}
	decoded, err := url.PathUnescape(encoded)
	if err != nil || !ValidFilePath(decoded) {
		return "", "", fmt.Errorf("invalid file path")
	}
	return strings.ToLower(task), decoded, nil
}
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for _, i := range []int{8, 13, 18, 23} {
		if value[i] != '-' {
			return false
		}
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

type FileChunk struct {
	Size     uint64 `json:"size"`
	Offset   uint64 `json:"offset"`
	Revision string `json:"revision"`
	Data     string `json:"data"`
}
type FileQuery struct {
	Path     string `json:"path" required:"true"`
	Offset   uint64 `json:"offset"`
	Revision string `json:"revision"`
}

func (q FileQuery) Validate() error {
	if !ValidFilePath(q.Path) || q.Offset > core.MaxSafeInteger {
		return fmt.Errorf("invalid file query")
	}
	return nil
}
