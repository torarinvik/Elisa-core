package main

import (
	"os"
	"reflect"
	"testing"
)

func TestSMTObserversPreserveEnvironmentKeyAndBypassReplay(t *testing.T) {
	observers := []string{"ELISA_SMT_RLIMIT_LOG", "ELISA_SMT_TRACE"}
	// Remove existing observers so the test also works in an instrumented shell.
	for _, name := range append(observers, "ELISACORE_BUILD_TIMING", "ELISACORE_CPUPROFILE", "ELISACORE_MEMPROFILE", "ELISACORE_EMIT_LL", "ELISA_SEMANTIC_INTERNAL_PARITY_OUT", "ELISA_BACKEND_PARITY_OUT", "ELISACORE_ANALYSIS_CACHE_DEBUG") {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	baseline := buildCacheEnvKey()
	if buildCacheRequestObserverEnabled() {
		t.Fatal("unexpected baseline observer")
	}
	for _, name := range observers {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "observer-destination-or-function")
			if got := buildCacheEnvKey(); !reflect.DeepEqual(got, baseline) {
				t.Fatalf("observer changed semantic environment identity: got %v, want %v", got, baseline)
			}
			if !buildCacheRequestObserverEnabled() {
				t.Fatal("observer could replay an object without producing requested logs")
			}
		})
	}
	// Solver choice, resource budget, and verdict-cache policy remain authoritative.
	for _, name := range []string{"ELISA_SMT_RLIMIT", "ELISA_Z3", "ELISACORE_SMT_CACHE_DECLINED"} {
		t.Run(name, func(t *testing.T) {
			before := buildCacheEnvKey()
			t.Setenv(name, "changed-smt-policy")
			if reflect.DeepEqual(buildCacheEnvKey(), before) {
				t.Fatal("semantic SMT setting omitted from identity")
			}
		})
	}
}
