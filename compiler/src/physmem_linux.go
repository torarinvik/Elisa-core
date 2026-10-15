//go:build linux

package main

import "syscall"

// physicalMemoryBytes reports installed RAM, or 0 when unknown.
func physicalMemoryBytes() uint64 {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0
	}
	return uint64(info.Totalram) * uint64(info.Unit)
}
