package main

import (
	"runtime/debug"
	"testing"
)

func TestDefaultGCPercentPolicy(t *testing.T) {
	if got := defaultGCPercentFor("darwin"); got != 300 {
		t.Fatalf("Darwin default GC percent = %d, want 300", got)
	}
	for _, goos := range []string{"linux", "windows", "freebsd"} {
		if got := defaultGCPercentFor(goos); got != defaultGCPercent {
			t.Errorf("%s default GC percent = %d, want %d", goos, got, defaultGCPercent)
		}
	}
}

func TestApplyDefaultGCPercentPreservesExplicitGOGC(t *testing.T) {
	t.Setenv("GOGC", "71")
	t.Setenv("GOMEMLIMIT", "off")
	previous := debug.SetGCPercent(71)
	t.Cleanup(func() { debug.SetGCPercent(previous) })

	applyDefaultGCPercent()
	if got := debug.SetGCPercent(-1); got != 71 {
		t.Fatalf("explicit GOGC was overridden: current percent before query = %d, want 71", got)
	}
}
