//go:build !darwin || !cgo

package main

import "errors"

func nativeCaptureSources() ([]byte, error) {
	return nil, errors.New("화면 선택은 macOS에서 사용할 수 있어요")
}
