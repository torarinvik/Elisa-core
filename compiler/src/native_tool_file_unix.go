//go:build darwin || freebsd || linux

package main

import (
	"os"
	"syscall"
)

func openToolchainStampFile(path string) (*os.File, error) {
	descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = syscall.Close(descriptor)
		return nil, os.ErrInvalid
	}
	return file, nil
}
