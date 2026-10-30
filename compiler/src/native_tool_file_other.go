//go:build !darwin && !freebsd && !linux

package main

import "os"

func openToolchainStampFile(path string) (*os.File, error) {
	return os.Open(path)
}
