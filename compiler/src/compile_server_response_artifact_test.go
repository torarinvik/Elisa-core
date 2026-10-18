//go:build cgo

package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCompileServerResponseRetainsConfiguredUnitWorkingSet(t *testing.T) {
	for _, units := range []int{2, 9, defaultCompileServerSessionCacheEntries} {
		t.Run(fmt.Sprintf("units_%d", units), func(t *testing.T) {
			cache := newCompileServerSessionCache(units)
			dir := t.TempDir()
			requests := make([]compileServerRequest, units)
			responses := make([]compileServerResponse, units)
			analyses := make([]uint64, units)
			for i := range requests {
				requests[i] = compileServerRequest{Mode: "obj", Filename: filepath.Join(dir, fmt.Sprintf("unit%d.elisa", i)), Source: "def main() -> i64:\n    return 7\n", Incremental: true}
				if i == 0 {
					requireServerNativeIdentity(t, requests[i], cache)
				}
				var status int
				responses[i], status = executeCompileServerRequestWithSessions(requests[i], cache)
				if status != http.StatusOK || !responses[i].OK {
					t.Fatalf("cold unit %d failed: %d %+v", i, status, responses[i])
				}
				analyses[i] = cache.session(requests[i].Filename, emitObject).Stats().Analyses
			}
			before, builds := cache.native.Stats(), cache.objects.Stats().Builds
			for i, req := range requests {
				response, status := executeCompileServerRequestWithSessions(req, cache)
				if status != http.StatusOK || response != responses[i] {
					t.Fatalf("warm unit %d changed response", i)
				}
				if cache.session(req.Filename, emitObject).Stats().Analyses != analyses[i] {
					t.Errorf("configured unit %d was evicted before its next build pass", i)
				}
			}
			after := cache.native.Stats()
			if after.ResponseHits-before.ResponseHits != uint64(units) || cache.objects.Stats().Builds != builds {
				t.Fatalf("working set lost exact responses: before=%+v after=%+v", before, after)
			}
			if after.Entries > units || after.Bytes > serverNativeCacheBytes {
				t.Fatalf("configured cache bounds exceeded: %+v", after)
			}
		})
	}
}

func TestCompileServerResponseReplayEditRevertAndDiagnostics(t *testing.T) {
	t.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", "0")
	cache := newCompileServerSessionCache(4)
	source := "@deprecated(\"use newer\")\n" + serverNativeArtifactSource()
	req := compileServerRequest{Mode: "obj", Filename: filepath.Join(t.TempDir(), "response.elisa"), Source: source, Incremental: true}
	requireServerNativeIdentity(t, req, cache)
	run := func() compileServerResponse {
		t.Helper()
		r, status := executeCompileServerRequestWithSessions(req, cache)
		if status != http.StatusOK || !r.OK {
			t.Fatalf("status=%d response=%+v", status, r)
		}
		return r
	}
	first := run()
	if !strings.Contains(first.Stderr, "use newer") {
		t.Fatal("cold warning missing")
	}
	session := cache.session(req.Filename, emitObject)
	assertReplay := func(want compileServerResponse) {
		t.Helper()
		analyses, backend := session.Stats(), cache.objects.Stats()
		cache.tokens.mu.Lock()
		tokenClock := cache.tokens.clock
		cache.tokens.mu.Unlock()
		cache.mu.Lock()
		sessionClock := cache.clock
		cache.mu.Unlock()
		hits := cache.native.Stats().ResponseHits
		if got := run(); got != want {
			t.Fatalf("full replay changed response: got=%+v want=%+v", got, want)
		}
		if session.Stats() != analyses || cache.objects.Stats() != backend || cache.native.Stats().ResponseHits != hits+1 {
			t.Fatal("exact response replay entered semantic or backend pipeline")
		}
		cache.tokens.mu.Lock()
		afterTokenClock := cache.tokens.clock
		cache.tokens.mu.Unlock()
		cache.mu.Lock()
		afterSessionClock := cache.clock
		cache.mu.Unlock()
		if afterTokenClock != tokenClock || afterSessionClock != sessionClock {
			t.Fatal("exact response replay entered token cache or semantic-session LRU")
		}
	}
	assertReplay(first)
	req.Source = strings.Replace(source, "return value + 1 +", "return value + 2 +", 1)
	second := run()
	if second.Object == first.Object {
		t.Fatal("body edit returned stale object")
	}
	assertReplay(second)
	req.Source = source
	assertReplay(first)
	// Failed requests never publish a successful response under another source.
	req.Source = strings.Replace(source, "return value + 1 +", "return missing + 1 +", 1)
	bad, status := executeCompileServerRequestWithSessions(req, cache)
	if status != http.StatusBadRequest || bad.OK || bad.Object != "" || !strings.Contains(bad.Stderr, "missing") {
		t.Fatalf("bad request hidden by response cache: status=%d response=%+v", status, bad)
	}
	req.Source = source
	assertReplay(first)
	// Warning policy is part of identity even though emitted machine code agrees.
	t.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", "1")
	withoutWarning := run()
	if strings.Contains(withoutWarning.Stderr, "use newer") {
		t.Fatal("warning-policy change reused stale diagnostics")
	}
	assertReplay(withoutWarning)
	t.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", "0")
	assertReplay(first)
	beforeDecline, hits := session.Stats().Analyses, cache.native.Stats().ResponseHits
	t.Setenv("ELISA_UNKNOWN_FUTURE_SWITCH", "1")
	run()
	if session.Stats().Analyses != beforeDecline+1 || cache.native.Stats().ResponseHits != hits {
		t.Fatal("unknown compiler configuration reused a completed response")
	}
}

func TestCompileServerResponseConcurrentWarmReplay(t *testing.T) {
	cache := newCompileServerSessionCache(4)
	req := compileServerRequest{Mode: "obj", Filename: filepath.Join(t.TempDir(), "concurrent-response.elisa"), Source: serverNativeArtifactSource(), Incremental: true}
	requireServerNativeIdentity(t, req, cache)
	want, status := executeCompileServerRequestWithSessions(req, cache)
	if status != http.StatusOK || !want.OK {
		t.Fatalf("cold request failed: %d %+v", status, want)
	}
	before := cache.session(req.Filename, emitObject).Stats()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, status := executeCompileServerRequestWithSessions(req, cache)
			if status != http.StatusOK || got != want {
				t.Errorf("concurrent replay mismatch: status=%d", status)
			}
		}()
	}
	wg.Wait()
	if cache.session(req.Filename, emitObject).Stats().Analyses != before.Analyses || cache.native.Stats().ResponseHits != 8 {
		t.Fatal("warm concurrent replay analyzed or missed responses")
	}
}

func BenchmarkCompileServerResponseNoop(b *testing.B) {
	for _, replay := range []bool{false, true} {
		name := "artifact_only"
		if replay {
			name = "complete_response"
		}
		b.Run(name, func(b *testing.B) {
			cache := newCompileServerSessionCache(4)
			cache.replayResponses = replay
			req := compileServerRequest{Mode: "obj", Filename: filepath.Join(b.TempDir(), "noop.elisa"), Source: serverNativeArtifactSource(), Incremental: true}
			requireServerNativeIdentity(b, req, cache)
			want, status := executeCompileServerRequestWithSessions(req, cache)
			if status != http.StatusOK || !want.OK {
				b.Fatalf("cold compile failed: %d %+v", status, want)
			}
			session := cache.session(req.Filename, emitObject)
			before := session.Stats()
			native := cache.objects.Stats()
			hits := cache.native.Stats().ResponseHits
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, status := executeCompileServerRequestWithSessions(req, cache)
				if status != http.StatusOK || got != want {
					b.Fatal("no-op response changed")
				}
			}
			b.StopTimer()
			wantAnalyses := uint64(b.N)
			if replay {
				wantAnalyses = 0
				if cache.native.Stats().ResponseHits-hits != uint64(b.N) {
					b.Fatal("response cache missed")
				}
			}
			if session.Stats().Analyses-before.Analyses != wantAnalyses || cache.objects.Stats() != native {
				b.Fatal("wrong validation or backend work")
			}
		})
	}
}
