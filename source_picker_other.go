//go:build !windows && (!darwin || !cgo)

package main

import "errors"

func chooseCaptureSource() (CaptureSource, error) {
	return CaptureSource{}, &CaptureError{Kind: CaptureUnavailable, Err: errors.New("Screen selection is only available on macOS")}
}

func platformRequestCapture() error {
	return errors.New("native screen selection requires macOS with cgo enabled")
}

func platformInstallMenu() error {
	return errors.New("native status menu requires macOS with cgo enabled")
}
