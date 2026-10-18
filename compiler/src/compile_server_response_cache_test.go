package main

import (
	"net/http"
	"strings"
	"sync"
	"testing"
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
				id := serverNativeIdentity{source: name, config: "config"}
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
