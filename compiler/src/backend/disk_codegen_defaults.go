package backend

import (
	"os"
	"strings"
)

// UseDiskIncrementalDefaults selects disk-backed object reuse for standalone
// disk-analysis requests. Optimized builds retain whole-module optimization;
// partitioned O3 optimization remains an explicit experiment. O0 can still use
// the independent IR path. Call before using the session.
func (s *IncrementalObjectSession) UseDiskIncrementalDefaults() {
	s.diskDefaults = true
	if _, explicit := os.LookupEnv("ELISACORE_OBJECT_PARTITIONS"); !explicit && s.diskDefaultsActive() {
		// 32 reduces per-module setup while keeping cache invalidation reasonably local.
		s.partitions = 32
	}
}

func configuredCodegenFlag(name string, fallback bool) bool {
	if value, explicit := os.LookupEnv(name); explicit {
		if name == "ELISACORE_PARALLEL_OPT" {
			value = strings.TrimSpace(value)
		}
		return value == "1"
	}
	return fallback
}

func (s *IncrementalObjectSession) diskDefaultsActive() bool {
	if !s.diskDefaults {
		return false
	}
	// Any explicit primary-mode setting opts out of the coherent preset.
	// A partial opt-in must not silently supply the other legacy mode flag.
	for _, name := range []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT"} {
		if _, explicit := os.LookupEnv(name); explicit {
			return false
		}
	}
	// The older full-module import mode is a distinct explicit experiment.
	if value, explicit := os.LookupEnv("ELISACORE_PARALLEL_OPT_IMPORT"); explicit && strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "0" {
		return false
	}
	return true
}

func (s *IncrementalObjectSession) diskCodegenFlag(name string) bool {
	fallback := s.diskDefaultsActive()
	// Partitioned IR and per-partition optimization reduce whole-program O3
	// visibility. Keep them opt-in even for disk-backed standalone builds.
	if name == "ELISACORE_PARALLEL_IR" || name == "ELISACORE_PARALLEL_OPT" {
		fallback = false
	}
	return configuredCodegenFlag(name, fallback)
}
