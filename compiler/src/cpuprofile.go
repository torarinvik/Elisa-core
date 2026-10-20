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
// short-lived maps on a large program while its live heap stays near 1 GB. Measured on the stage1
// driver (Linux box, 2026-10-15, user CPU / peak RSS): GOGC 100 352 s / 3.6 GB, 400 163 s / 8.0 GB,
// 800 134 s / 12.4 GB, 1200 126 s / 13.7 GB, 1600 127 s / 18.1 GB. 1200 is the knee (owner rule:
// speed over memory). An explicit GOGC in the environment still wins.
const defaultGCPercent = 1200

// darwinGCPercent replaces defaultGCPercent on macOS, where a fresh heap page costs a slow fault
// (madvise + zeroing were ~60% of a seed's CPU profile at 1200). Measured on an M5 (2026-10-20,
// seed, wall / peak RSS): 1200 41.3-42.6 s / 9.6 GB, 300 39.9 s / 7.5 GB, 200 39.3 s / 6.0 GB.
const darwinGCPercent = 200

// defaultMemoryLimitFraction caps the soft heap limit at this share of installed RAM, so the
// generous GOGC tightens collection instead of swapping on a smaller machine. An explicit
// GOMEMLIMIT in the environment still wins; with RAM unknown no limit is set.
const defaultMemoryLimitFraction = 0.6

func applyDefaultGCPercent() {
	if _, set := os.LookupEnv("GOGC"); !set {
		percent := defaultGCPercent
		if runtime.GOOS == "darwin" {
			percent = darwinGCPercent
		}
		debug.SetGCPercent(percent)
	}
	if _, set := os.LookupEnv("GOMEMLIMIT"); !set {
		if ram := physicalMemoryBytes(); ram > 0 {
			debug.SetMemoryLimit(int64(float64(ram) * defaultMemoryLimitFraction))
		}
	}
}
