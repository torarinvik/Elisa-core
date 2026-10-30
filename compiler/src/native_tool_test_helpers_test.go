package main

import "runtime"

func nativeToolFixturesSupported() bool {
	switch runtime.GOOS {
	case "darwin", "freebsd", "linux":
		return true
	default:
		return false
	}
}

func nativeToolTestFixture(contents string) []byte {
	magic := []byte("\x7fELF")
	if runtime.GOOS == "darwin" {
		magic = []byte("\xcf\xfa\xed\xfe")
	}
	return append(append([]byte(nil), magic...), []byte(contents)...)
}
