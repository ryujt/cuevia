//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Foundation -framework CoreGraphics
#include <stdlib.h>
char *cuevia_copy_sources(void);
*/
import "C"
import (
	"errors"
	"unsafe"
)

func nativeCaptureSources() ([]byte, error) {
	value := C.cuevia_copy_sources()
	if value == nil {
		return nil, errors.New("화면 목록을 읽지 못했어요")
	}
	defer C.free(unsafe.Pointer(value))
	return []byte(C.GoString(value)), nil
}
