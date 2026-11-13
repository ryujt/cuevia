//go:build !darwin || !cgo

package main

import (
	"context"
	"errors"
)

func runSelectedScreenCapture(ctx context.Context, source CaptureSource, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, &CaptureError{Kind: CaptureCanceled, Err: err}
	}
	return nil, &CaptureError{Kind: CaptureUnavailable, Err: errors.New("ScreenCaptureKit screen capture requires macOS 14 or later")}
}
