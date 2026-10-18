package main

import (
	"strings"
	"testing"
)

func TestServerNativeCacheExactIdentityAndLRU(t *testing.T) {
	c := newCompileServerNativeCache(nil, 2, 4096)
	// Deliberate digest collisions must still compare complete source/config.
	a := serverNativeIdentity{source: "one", config: "A"}
	b := serverNativeIdentity{source: "two", config: "A"}
	d := serverNativeIdentity{source: "three", config: "A"}
	if !c.retain(a, "object-one") || !c.retain(b, "object-two") {
		t.Fatal("admission")
	}
	if got, ok := c.lookup(a); !ok || got != "object-one" {
		t.Fatal("wrong object on digest collision")
	}
	if !c.retain(d, "object-three") {
		t.Fatal("third admission")
	}
	if _, ok := c.lookup(b); ok {
		t.Fatal("least recently used object retained")
	}
	if got, ok := c.lookup(d); !ok || got != "object-three" {
		t.Fatal("new object missing")
	}
	changed := a
	changed.config = "B"
	if _, ok := c.lookup(changed); ok {
		t.Fatal("configuration change reused output")
	}
	if c.retain(changed, strings.Repeat("x", 4096)) {
		t.Fatal("oversized artifact admitted")
	}
	stats := c.Stats()
	if stats.Bytes > 4096 || stats.Entries != 2 || stats.Evictions != 1 {
		t.Fatalf("bounds: %+v", stats)
	}
}

func TestServerNativeCacheByteBudgetAndDefaultsGate(t *testing.T) {
	c := newCompileServerNativeCache(nil, 8, 1200)
	for i := 0; i < 8; i++ {
		id := serverNativeIdentity{source: strings.Repeat(string(rune('a'+i)), 30), config: "cfg"}
		if !c.retain(id, strings.Repeat("o", 100)) {
			t.Fatal("admission")
		}
		if s := c.Stats(); s.Bytes > 1200 || s.Entries > 8 {
			t.Fatal(s)
		}
	}
	if !serverNativeEnvironmentAllowed([]string{"PATH=/usr/bin", "HOME=/tmp", "ELISACORE_SUPPRESS_DEPRECATED_WARNINGS=1"}) {
		t.Fatal("ordinary configuration declined")
	}
	if serverNativeEnvironmentAllowed([]string{"LANG=\xff"}) {
		t.Fatal("non-text configuration normalized into key")
	}
	for _, name := range []string{"ELISACORE_FORCE_CONTRACTS", "ELISA_LINEAR_MUTATION", "ELISA_UNKNOWN_FUTURE_SWITCH", "LLVM_PROFILE_FILE", "DYLD_INSERT_LIBRARIES", "LD_PRELOAD", "SDKROOT", "DEVELOPER_DIR", "TOOLCHAINS"} {
		if serverNativeEnvironmentAllowed([]string{name + "="}) {
			t.Fatalf("unclosed configuration allowed: %s", name)
		}
	}
}
