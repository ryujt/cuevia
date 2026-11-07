//go:build !windows && (!darwin || !cgo)

package main

import "errors"

func chooseCaptureSource() (CaptureSource, error) {
	return CaptureSource{}, &CaptureError{Kind: CaptureUnavailable, Err: errors.New("화면 선택은 macOS에서 사용할 수 있어요")}
}

func platformRequestCapture() error {
	return errors.New("native screen selection requires macOS with cgo enabled")
}

func platformInstallMenu() error {
	return errors.New("native status menu requires macOS with cgo enabled")
}
