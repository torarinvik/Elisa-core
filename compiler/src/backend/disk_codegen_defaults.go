package backend

import (
	"os"
	"strings"
)

// UseDiskIncrementalDefaults selects the qualified native pipeline for a
// standalone disk-analysis request. Call before using the session. The ordinary
// backend/serial/textual-IR entry points retain their existing defaults.
// Explicit environment settings always keep their original meanings.
func (s *IncrementalObjectSession) UseDiskIncrementalDefaults() {
	s.diskDefaults = true
	if _, explicit := os.LookupEnv("ELISACORE_OBJECT_PARTITIONS"); !explicit && s.diskDefaultsActive() {
		s.partitions = 96
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
	// An explicit primary-mode override opts out of the coherent preset. In
	// particular, IR=0 must not silently enable legacy parallel optimization.
	for _, name := range []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT"} {
		if !configuredCodegenFlag(name, true) {
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
	return configuredCodegenFlag(name, s.diskDefaultsActive())
}
