//go:build !darwin || !cgo

package main

import (
	"context"
	"errors"
)

func platformRun(ctx context.Context, onCapture, onSettings func(), ready ...func()) error {
	return errors.New("native global shortcut requires macOS with cgo enabled")
}

func screenCapturePermissionGranted() bool { return false }
