//go:build !darwin && !linux

package main

// physicalMemoryBytes reports installed RAM, or 0 when unknown.
func physicalMemoryBytes() uint64 { return 0 }
