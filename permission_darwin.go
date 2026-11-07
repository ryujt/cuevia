//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Cocoa -framework CoreGraphics
#include <stdlib.h>
int cuevia_request_screen_permission(int *allowed);
int cuevia_show_capture_error(const char *message);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// platformRequestScreenCapturePermission must run on the macOS main thread.
// A false result is a permission decision, not a failure to call the API.
func platformRequestScreenCapturePermission() (bool, error) {
	var allowed C.int
	if status := C.cuevia_request_screen_permission(&allowed); status != 0 {
		return false, fmt.Errorf("request screen capture permission (macOS status %d)", int(status))
	}
	return allowed != 0, nil
}

// platformShowCaptureError presents an actionable failure on the main thread.
func platformShowCaptureError(message string) error {
	value := C.CString(message)
	defer C.free(unsafe.Pointer(value))
	if status := C.cuevia_show_capture_error(value); status != 0 {
		return fmt.Errorf("show capture failure (macOS status %d)", int(status))
	}
	return nil
}
