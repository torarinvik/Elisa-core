package main

import (
	"runtime"
	"testing"
)

func TestPhysicalMemoryBytesKnownOnSupportedHosts(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("RAM probe only implemented for darwin and linux")
	}
	ram := physicalMemoryBytes()
	if ram < 1<<30 || ram > 1<<44 {
		t.Fatalf("physicalMemoryBytes() = %d, want between 1 GiB and 16 TiB", ram)
	}
}
