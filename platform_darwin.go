//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Carbon -framework ApplicationServices
#include "native_darwin.h"
*/
import "C"

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync"
)

// Carbon registration and event dispatch belong to the initial macOS thread.
// Locking during init keeps main on that thread, including before platformRun.
func init() { runtime.LockOSThread() }

var nativeCapture struct {
	sync.RWMutex
	callback func()
	settings func()
}

// platformRun runs on the main goroutine until cancellation. onCapture must
// enqueue work and return immediately; interactive capture runs in a worker.
func platformRun(ctx context.Context, onCapture, onSettings func(), ready ...func()) error {
	if ctx.Err() != nil {
		return nil
	}
	nativeCapture.Lock()
	nativeCapture.callback = onCapture
	nativeCapture.settings = onSettings
	nativeCapture.Unlock()
	defer func() {
		nativeCapture.Lock()
		nativeCapture.callback = nil
		nativeCapture.settings = nil
		nativeCapture.Unlock()
	}()
	if err := platformInstallMenu(); err != nil {
		return err
	}
	if status := C.cuevia_start_hotkey(); status != 0 {
		if status == -20001 {
			return fmt.Errorf("register global shortcut: platformRun must run on the macOS main thread")
		}
		return fmt.Errorf("register Command+Control+S global shortcut (macOS status %d)", int(status))
	}
	log.Print("Cuevia native global shortcut registered: Command+Control+S (main thread, background)")
	defer C.cuevia_stop_hotkey()
	for _, callback := range ready {
		if callback != nil {
			callback()
		}
	}
	for ctx.Err() == nil {
		if status := C.cuevia_dispatch_next_event(); status != 0 {
			return fmt.Errorf("dispatch native shortcut event (macOS status %d)", int(status))
		}
	}
	return nil
}

//export cueviaCapturePressed
func cueviaCapturePressed() {
	nativeCapture.RLock()
	callback := nativeCapture.callback
	nativeCapture.RUnlock()
	if callback != nil {
		callback()
	}
}

//export cueviaSettingsPressed
func cueviaSettingsPressed() {
	nativeCapture.RLock()
	callback := nativeCapture.settings
	nativeCapture.RUnlock()
	if callback != nil {
		callback()
	}
}

func screenCapturePermissionGranted() bool { return C.cuevia_screen_capture_allowed() != 0 }
