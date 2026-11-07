package main

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

func chooseCaptureSource() (CaptureSource, error) {
	return CaptureSource{}, errors.New("use the Windows tray capture selector")
}
func platformInstallMenu() error { return nil }
func platformRequestCapture() error {
	name := os.Getenv("CUEVIA_CAPTURE_EVENT")
	if name == "" {
		return errors.New("Windows tray is not running")
	}
	wide, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	handle, _, callErr := kernel.NewProc("OpenEventW").Call(2, 0, uintptr(unsafe.Pointer(wide)))
	if handle == 0 {
		return callErr
	}
	defer syscall.CloseHandle(syscall.Handle(handle))
	ok, _, callErr := kernel.NewProc("SetEvent").Call(handle)
	if ok == 0 {
		return callErr
	}
	return nil
}
