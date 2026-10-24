//go:build !linux

package executor

import "errors"

func writePatchInPlace(string, []byte, *patchOriginal) error {
	return errors.New("planned in-place patch writes require Linux")
}
