//go:build !darwin || !cgo

package main

import "errors"

func nativeCaptureSources() ([]byte, error) {
	return nil, errors.New("Screen selection is only available on macOS")
}
