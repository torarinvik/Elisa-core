package main

import (
	"os"
	"runtime"
	"runtime/debug"
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

// defaultGCPercent trades heap headroom for collector time. The analyzer allocates tens of GB of
// short-lived maps on a large program while its live heap stays near 1 GB; at Go's default 100
// the collector was ~30% of CPU on the stage1 driver. 400 cut instructions retired by ~11% and
// user time by ~17% there, raising peak heap from ~2 GB to ~5 GB (owner rule: speed over
// memory). An explicit GOGC in the environment still wins.
const defaultGCPercent = 400

func applyDefaultGCPercent() {
	if _, set := os.LookupEnv("GOGC"); set {
		return
	}
	debug.SetGCPercent(defaultGCPercent)
}
