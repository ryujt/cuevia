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
		return nil, errors.New("Couldn't read the list of screens")
	}
	defer C.free(unsafe.Pointer(value))
	return []byte(C.GoString(value)), nil
}
