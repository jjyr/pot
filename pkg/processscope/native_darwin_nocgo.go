//go:build darwin && !cgo

package processscope

import (
	"context"
	"errors"
)

func prepareNative() error {
	return errors.New("macOS execution supervision requires the packaged native build")
}

type nativeObserver struct{}

func observeNative(int, Markers) (*nativeObserver, error) { return nil, prepareNative() }
func (*nativeObserver) Close()                            {}
func (*nativeObserver) Drain(context.Context) error       { return prepareNative() }
