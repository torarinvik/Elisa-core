package main

import (
	"runtime"
	"testing"
)

// platformLinkFlagsFor selects the host platform's link flags from a per-target
// "platforms" map, normalizing "darwin" -> "macos". A nil/empty map yields no flags.
func TestPlatformLinkFlagsForSelectsHostOS(t *testing.T) {
	platforms := map[string]projectPlatformOverride{
		"macos": {LinkFlags: []string{"-lZydis"}},
		"linux": {LinkFlags: []string{"-Wl,--no-as-needed", "-lZydis", "-Wl,--as-needed"}},
	}
	var want []string
	switch runtime.GOOS {
	case "darwin":
		want = []string{"-lZydis"}
	case "linux":
		want = []string{"-Wl,--no-as-needed", "-lZydis", "-Wl,--as-needed"}
	default:
		want = nil // no entry for this host; selector must return nil
	}
	got := platformLinkFlagsFor(platforms)
	if len(got) != len(want) {
		t.Fatalf("platformLinkFlagsFor on %s: got %v, want %v", runtime.GOOS, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("platformLinkFlagsFor on %s: got %v, want %v", runtime.GOOS, got, want)
		}
	}
}

func TestPlatformLinkFlagsForEmptyAndUnmatched(t *testing.T) {
	if got := platformLinkFlagsFor(nil); got != nil {
		t.Fatalf("nil platforms map must yield nil, got %v", got)
	}
	// A map with no entry for the host platform yields nil.
	if got := platformLinkFlagsFor(map[string]projectPlatformOverride{"plan9": {LinkFlags: []string{"-lx"}}}); got != nil {
		t.Fatalf("unmatched host platform must yield nil, got %v", got)
	}
}
