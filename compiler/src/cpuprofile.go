package main

import (
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"
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
	// A bounded sample is useful when an external timeout stops a slow compile:
	// flush the profile before that timeout instead of losing the final gzip data.
	var once sync.Once
	if os.Getenv("ELISACORE_BLOCKPROFILE") != "" {
		runtime.SetBlockProfileRate(1)
	}
	stop := func() {
		once.Do(func() {
			pprof.StopCPUProfile()
			f.Close()
			for _, profile := range []struct {
				name, path string
				debug      int
			}{
				{"goroutine", os.Getenv("ELISACORE_GOROUTINEPROFILE"), 2},
				{"block", os.Getenv("ELISACORE_BLOCKPROFILE"), 0},
			} {
				if profile.path == "" {
					continue
				}
				output, err := os.Create(profile.path)
				if err == nil {
					_ = pprof.Lookup(profile.name).WriteTo(output, profile.debug)
					output.Close()
				}
			}
		})
	}
	var timer *time.Timer
	if seconds, err := strconv.ParseFloat(os.Getenv("ELISACORE_CPUPROFILE_SECONDS"), 64); err == nil && seconds > 0 && seconds <= 86400 {
		timer = time.AfterFunc(time.Duration(seconds*float64(time.Second)), stop)
	}
	return func() {
		if timer != nil {
			timer.Stop()
		}
		stop()
	}
}

// startMemProfile enables full allocation sampling when $ELISACORE_MEMPROFILE is set and returns
// a func that writes the heap profile (use -sample_index=alloc_space) there. Developer-only.
func startMemProfile() func() {
	path := os.Getenv("ELISACORE_MEMPROFILE")
	if path == "" {
		return func() {}
	}
	runtime.MemProfileRate = 64 * 1024
	var once sync.Once
	write := func() {
		once.Do(func() {
			f, err := os.Create(path)
			if err != nil {
				return
			}
			defer f.Close()
			_ = pprof.Lookup("allocs").WriteTo(f, 0)
		})
	}
	var timer *time.Timer
	if seconds, err := strconv.ParseFloat(os.Getenv("ELISACORE_MEMPROFILE_SECONDS"), 64); err == nil && seconds > 0 && seconds <= 86400 {
		timer = time.AfterFunc(time.Duration(seconds*float64(time.Second)), write)
	}
	return func() {
		if timer != nil {
			timer.Stop()
		}
		write()
	}

}

// defaultGCPercent trades heap headroom for collector time. The analyzer allocates tens of GB of
// short-lived maps on a large program while its live heap stays near 1 GB. Measured on the stage1
// driver (Linux box, 2026-10-15, user CPU / peak RSS): GOGC 100 352 s / 3.6 GB, 400 163 s / 8.0 GB,
// 800 134 s / 12.4 GB, 1200 126 s / 13.7 GB, 1600 127 s / 18.1 GB. 1200 is the knee (owner rule:
// speed over memory). An explicit GOGC in the environment still wins.
const defaultGCPercent = 1200

// darwinGCPercent replaces defaultGCPercent on macOS. On the Stage1 self-compile body-edit
// workload (M5, 2026-10-28, 96 O0 partitions, disk caches, no server), three runs measured a
// 4.92 s median CLI wall at 500 versus 5.39 s at 300; partitioned IR+object time was 2.55 s
// versus 3.10 s. Both reused all native partitions and produced the same object and diagnostics.
// Peak RSS increased by 68 MiB (7.16 versus 7.08 GB) under the existing 60%-of-RAM soft limit.
// Explicit GOGC still wins.
const darwinGCPercent = 500

// defaultMemoryLimitFraction caps the soft heap limit at this share of installed RAM, so the
// generous GOGC tightens collection instead of swapping on a smaller machine. An explicit
// GOMEMLIMIT in the environment still wins; with RAM unknown no limit is set.
const defaultMemoryLimitFraction = 0.6

func applyDefaultGCPercent() {
	if _, set := os.LookupEnv("GOGC"); !set {
		debug.SetGCPercent(defaultGCPercentFor(runtime.GOOS))
	}
	if _, set := os.LookupEnv("GOMEMLIMIT"); !set {
		if ram := physicalMemoryBytes(); ram > 0 {
			debug.SetMemoryLimit(int64(float64(ram) * defaultMemoryLimitFraction))
		}
	}
}

func defaultGCPercentFor(goos string) int {
	if goos == "darwin" {
		return darwinGCPercent
	}
	return defaultGCPercent
}
