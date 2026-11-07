//go:build !darwin || !cgo

package main

import "errors"

func platformRequestScreenCapturePermission() (bool, error) {
	return false, errors.New("native screen capture permission requires macOS with cgo enabled")
}

func platformShowCaptureError(message string) error {
	return errors.New(message)
}
