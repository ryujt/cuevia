//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework ScreenCaptureKit -framework ImageIO -framework Foundation -framework CoreGraphics -framework CoreVideo
#include <stdint.h>
#include <stdlib.h>
int cuevia_capture_selected(int kind, uint32_t display_id, uint32_t window_id, int pid,
    int width, int height, int timeout_ms, void **png, size_t *png_size, char **message);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
	"unsafe"
)

// runSelectedScreenCapture captures inside Cuevia's own TCC identity. Native
// callbacks only produce in-memory PNG data; only this caller writes its owned
// temporary file, after a successful result and a fresh context check.
func runSelectedScreenCapture(ctx context.Context, source CaptureSource, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, &CaptureError{Kind: CaptureCanceled, Err: err}
	}
	kind := 0
	switch source.Kind {
	case "monitor":
		if source.DisplayID != 0 && source.Width > 0 && source.Height > 0 && source.Width <= 32768 && source.Height <= 32768 && int64(source.Width)*int64(source.Height) <= 40000000 {
			kind = 1
		}
	case "program":
		if source.WindowID != 0 && source.PID > 0 {
			kind = 2
		}
	}
	if kind == 0 {
		return nil, &CaptureError{Kind: CaptureFailed, Err: errors.New("The screen to capture is invalid")}
	}
	wait := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(deadline))
	}
	if wait <= 0 {
		return nil, &CaptureError{Kind: CaptureCanceled, Err: context.DeadlineExceeded}
	}
	var png unsafe.Pointer
	var size C.size_t
	var message *C.char
	status := C.cuevia_capture_selected(C.int(kind), C.uint32_t(source.DisplayID), C.uint32_t(source.WindowID), C.int(source.PID), C.int(source.Width), C.int(source.Height), C.int((wait+time.Millisecond-1)/time.Millisecond), &png, &size, &message)
	if png != nil {
		defer C.free(png)
	}
	if message != nil {
		defer C.free(unsafe.Pointer(message))
	}
	if err := ctx.Err(); err != nil {
		return nil, &CaptureError{Kind: CaptureCanceled, Err: err}
	}
	if status != 0 {
		kind := CaptureFailed
		if status == -3801 {
			kind = CapturePermissionDenied
		} else if status == -3900 {
			kind = CaptureUnavailable
		}
		details := "Couldn't capture the screen"
		if message != nil {
			details = C.GoString(message)
		}
		return nil, &CaptureError{Kind: kind, Err: fmt.Errorf("ScreenCaptureKit (%d): %s", int(status), details)}
	}
	if png == nil || size == 0 || uint64(size) > 32<<20 {
		return nil, &CaptureError{Kind: CaptureFailed, Err: errors.New("The captured PNG data is invalid")}
	}
	data := C.GoBytes(png, C.int(size))
	if err := ctx.Err(); err != nil {
		return nil, &CaptureError{Kind: CaptureCanceled, Err: err}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, &CaptureError{Kind: CaptureFailed, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return nil, &CaptureError{Kind: CaptureCanceled, Err: err}
	}
	return nil, nil
}
