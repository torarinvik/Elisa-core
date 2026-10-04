package main

import (
	"os"
	"runtime"
	"runtime/pprof"
)

// startCPUProfile writes a pprof CPU profile to $ELISACORE_CPUPROFILE when set.
// Developer-only; has no effect on compiler output.
func startCPUProfile() func() {
	path := os.Getenv("ELISACORE_CPUPROFILE")
	if path == "" {
		return func() {}
	}
	f, err := os.Create(path)
	if err != nil {
		return func() {}
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return func() {}
	}
	return func() { pprof.StopCPUProfile(); f.Close() }
}

// startMemProfile enables full allocation sampling when $ELISACORE_MEMPROFILE is set and returns
// a func that writes the heap profile (use -sample_index=alloc_space) there. Developer-only.
func startMemProfile() func() {
	path := os.Getenv("ELISACORE_MEMPROFILE")
	if path == "" {
		return func() {}
	}
	runtime.MemProfileRate = 64 * 1024
	return func() {
		f, err := os.Create(path)
		if err != nil {
			return
		}
		_ = pprof.Lookup("allocs").WriteTo(f, 0)
		f.Close()
	}
}
