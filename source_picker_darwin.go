//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework QuartzCore
#include <stdlib.h>
char *cuevia_choose_source(int *status);
int cuevia_request_capture(void);
int cuevia_install_menu(void);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"
)

// chooseCaptureSource must run synchronously on the application's macOS main
// thread. The native picker restores the previous app before capture starts.
func chooseCaptureSource() (CaptureSource, error) {
	var status C.int
	value := C.cuevia_choose_source(&status)
	if value == nil {
		kind := CaptureUnavailable
		message := "Couldn't open the screen picker"
		if status == 1 {
			kind, message = CaptureCanceled, "Screen selection canceled"
		}
		return CaptureSource{}, &CaptureError{Kind: kind, Err: errors.New(message)}
	}
	defer C.free(unsafe.Pointer(value))
	var source CaptureSource
	if err := json.Unmarshal([]byte(C.GoString(value)), &source); err != nil || source.ID == "" || (source.Kind != "monitor" && source.Kind != "program") {
		return CaptureSource{}, &CaptureError{Kind: CaptureFailed, Err: errors.New("Couldn't read the selected screen")}
	}
	return source, nil
}

// platformRequestCapture schedules the same handler as the global shortcut;
// HTTP workers never open AppKit UI on a background thread.
func platformRequestCapture() error {
	if status := C.cuevia_request_capture(); status != 0 {
		return fmt.Errorf("request native capture (macOS status %d)", int(status))
	}
	return nil
}

func platformInstallMenu() error {
	if status := C.cuevia_install_menu(); status != 0 {
		return fmt.Errorf("install Cuevia menu (macOS status %d)", int(status))
	}
	return nil
}
