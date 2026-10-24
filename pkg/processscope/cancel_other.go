//go:build !darwin || !cgo

package processscope

import "os"

func cancelOriginal(_ *nativeObserver, original *os.Process, done <-chan error) error {
	return stopOriginal(original, done)
}
