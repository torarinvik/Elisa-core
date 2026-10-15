//go:build darwin

package main

import (
	"encoding/binary"
	"syscall"
)

// physicalMemoryBytes reports installed RAM, or 0 when unknown.
func physicalMemoryBytes() uint64 {
	raw, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}
	buf := []byte(raw)
	// Sysctl strips one trailing NUL; restore the 8-byte little-endian value.
	for len(buf) < 8 {
		buf = append(buf, 0)
	}
	return binary.LittleEndian.Uint64(buf[:8])
}
