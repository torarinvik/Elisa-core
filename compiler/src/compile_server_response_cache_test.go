package main

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

// Existing artifact and semantic-session tests deliberately exercise validation
// on every request. The complete-response tests exercise the new earlier tier.
func newCompileServerArtifactOnlyTestCache(entries int) *compileServerSessionCache {
	cache := newCompileServerSessionCache(entries)
	cache.replayResponses = false
	return cache
}

func TestServerResponseCacheConcurrentRetentionAndEviction(t *testing.T) {
	c := newCompileServerNativeCache(nil, 4, 4096)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				// Every entry deliberately shares a digest bucket.
				name := strings.Repeat(string(rune('a'+worker)), i%4+1)
				id := serverNativeIdentity{source: name, config: "config", unitHint: [32]byte{byte(worker + 1)}}
				want := compileServerResponse{OK: true, Object: name, Stderr: "warning:" + name}
				c.retainResponse(id, want, http.StatusOK)
				if got, status, hit := c.lookupResponse(id); hit && (got != want || status != http.StatusOK) {
					t.Error("concurrent retention/eviction mixed responses")
				}
			}
		}(worker)
	}
	wg.Wait()
	if s := c.Stats(); s.Bytes > 4096 || s.Entries > 4 {
		t.Fatalf("concurrent shared bounds exceeded: %+v", s)
	}
	count := 0
	for _, n := range c.unitCounts {
		count += n
	}
	if count != c.lru.Len() || c.historyEntries != count-len(c.unitCounts) {
		t.Fatal("unit history accounting drifted during concurrent eviction")
	}
}

func TestServerResponseCacheEvictsHistoryBeforeUniqueUnits(t *testing.T) {
	c := newCompileServerNativeCache(nil, 3, 4096)
	id := func(unit byte, source string) serverNativeIdentity {
		return serverNativeIdentity{source: source, config: "config", unitHint: [32]byte{unit}}
	}
	a0, a1, b0, c0 := id(1, "a0"), id(1, "a1"), id(2, "b0"), id(3, "c0")
	for _, key := range []serverNativeIdentity{a0, a1, b0} {
		c.retain(key, key.source)
	}
	c.lookup(a0) // a0 is the most recently requested revision of its unit.
	c.retain(c0, c0.source)
	if _, hit := c.lookup(a1); hit {
		t.Fatal("older revision retained instead of another unit's sole result")
	}
	for _, key := range []serverNativeIdentity{a0, b0, c0} {
		if _, hit := c.lookup(key); !hit {
			t.Fatal("unique current unit evicted while history was available")
		}
	}
	a2 := id(1, "a2")
	c.retain(a2, a2.source)
	for _, key := range []serverNativeIdentity{a2, b0, c0} {
		if _, hit := c.lookup(key); !hit {
			t.Fatal("new revision displaced another current unit")
		}
	}
	if c.historyEntries != 0 || len(c.unitCounts) != 3 {
		t.Fatal("history metadata not retired")
	}
}

func TestServerResponseCacheProtectsObjectWhileAttachingDiagnostics(t *testing.T) {
	c := newCompileServerNativeCache(nil, 3, 2200)
	a := serverNativeIdentity{source: "a", config: "config", unitHint: [32]byte{1}}
	b := serverNativeIdentity{source: "b", config: "config", unitHint: [32]byte{2}}
	c.retain(a, "a")
	c.retain(b, "b")
	want := compileServerResponse{OK: true, Object: "b", Stderr: strings.Repeat("w", 1400)}
	if !c.retainResponse(b, want, http.StatusOK) {
		t.Fatal("bounded diagnostic attachment declined")
	}
	if got, _, hit := c.lookupResponse(b); !hit || got != want {
		t.Fatal("diagnostic attachment evicted its own object")
	}
	if _, hit := c.lookup(a); hit || c.Stats().Bytes > 2200 {
		t.Fatal("byte pressure did not evict another entry safely")
	}
}

func TestServerResponseCachePreservesFieldsAndExactIdentity(t *testing.T) {
	c := newCompileServerNativeCache(nil, 2, 4096)
	id := serverNativeIdentity{source: "one", config: "config"}
	want := compileServerResponse{OK: true, Object: "object", Output: "output", IR: "ir", Value: "value", Stderr: "warning", Error: "error", ErrorCode: "code"}
	if !c.retainResponse(id, want, http.StatusOK) {
		t.Fatal("response not admitted")
	}
	got, status, hit := c.lookupResponse(id)
	if !hit || status != http.StatusOK || got != want {
		t.Fatalf("incomplete response: %+v status=%d hit=%v", got, status, hit)
	}
	for _, other := range []serverNativeIdentity{{source: "two", config: "config"}, {source: "one", config: "different"}} {
		if _, _, hit := c.lookupResponse(other); hit {
			t.Fatal("digest collision bypassed exact source/config check")
		}
	}
	got.Stderr = "caller mutation"
	again, _, _ := c.lookupResponse(id)
	if again != want {
		t.Fatal("caller modified retained response")
	}
	if c.retainResponse(id, compileServerResponse{OK: false, Object: "bad"}, http.StatusBadRequest) {
		t.Fatal("failed response admitted")
	}
}

func TestServerResponseCacheBudgetsDiagnosticsAndKeepsArtifactFallback(t *testing.T) {
	c := newCompileServerNativeCache(nil, 2, 1024)
	id := serverNativeIdentity{source: "source", config: "config"}
	response := compileServerResponse{OK: true, Object: "object", Stderr: strings.Repeat("w", 600)}
	if c.retainResponse(id, response, http.StatusOK) {
		t.Fatal("diagnostics escaped shared byte budget")
	}
	if _, _, hit := c.lookupResponse(id); hit {
		t.Fatal("incomplete response became eligible for early reuse")
	}
	if object, hit := c.lookup(id); !hit || object != response.Object {
		t.Fatal("oversized diagnostics lost ordinary object fallback")
	}
	response.Stderr = "small warning"
	if !c.retainResponse(id, response, http.StatusOK) {
		t.Fatal("bounded response not admitted")
	}
	if stats := c.Stats(); stats.Bytes > 1024 || stats.Entries != 1 {
		t.Fatalf("shared cache bounds: %+v", stats)
	}
}

func TestServerResponseCacheDoesNotRewriteFinalizedObject(t *testing.T) {
	c := newCompileServerNativeCache(nil, 2, 4096)
	id := serverNativeIdentity{source: "source", config: "config"}
	c.retain(id, "earlier object")
	if c.retainResponse(id, compileServerResponse{OK: true, Object: "different finalized object"}, http.StatusOK) {
		t.Fatal("cache substituted object bytes in finalized response")
	}
	if _, _, hit := c.lookupResponse(id); hit {
		t.Fatal("mismatched response became eligible for replay")
	}
}

// The byte budget counts field lengths, so retained substrings must not pin a
// caller's much larger backing allocation. Object and response tiers should
// nevertheless share the cache-owned object allocation.
func TestServerResponseCacheOwnsRetainedStringStorage(t *testing.T) {
	parent := strings.Repeat("abcdefghijklmnopqrstuvwxyz", 1<<16)
	field := func(index int) string { return parent[index*32 : index*32+16] }
	id := serverNativeIdentity{source: field(0), config: field(1)}
	want := compileServerResponse{OK: true, Object: field(2), Output: field(3), IR: field(4), Value: field(5), Stderr: field(6), Error: field(7), ErrorCode: field(8)}
	c := newCompileServerNativeCache(nil, 1, 4096)
	if !c.retainResponse(id, want, http.StatusOK) {
		t.Fatal("response not admitted")
	}
	got, status, hit := c.lookupResponse(id)
	if !hit || status != http.StatusOK || got != want {
		t.Fatal("retained response changed")
	}
	entry := c.lru.Front().Value.(*serverNativeEntry)
	for _, pair := range []struct{ name, original, retained string }{
		{"source", id.source, entry.identity.source},
		{"config", id.config, entry.identity.config},
		{"object", want.Object, got.Object},
		{"output", want.Output, got.Output},
		{"IR", want.IR, got.IR},
		{"value", want.Value, got.Value},
		{"stderr", want.Stderr, got.Stderr},
		{"error", want.Error, got.Error},
		{"error code", want.ErrorCode, got.ErrorCode},
	} {
		if pair.original != pair.retained {
			t.Errorf("%s contents changed", pair.name)
		}
		if unsafe.StringData(pair.original) == unsafe.StringData(pair.retained) {
			t.Errorf("%s still borrows caller storage", pair.name)
		}
	}
	object, hit := c.lookup(id)
	if !hit || unsafe.StringData(object) != unsafe.StringData(got.Object) {
		t.Fatal("response did not share cache-owned object storage")
	}
}
