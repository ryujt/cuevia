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
	return nil, &CaptureError{Kind: CaptureUnavailable, Err: errors.New("ScreenCaptureKit 화면 캡처는 macOS 14 이상에서 사용할 수 있어요")}
}
