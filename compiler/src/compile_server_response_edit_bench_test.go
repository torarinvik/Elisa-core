//go:build cgo

package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every request is a novel fixed-width edit, so neither whole-object nor
// complete-response hits can hide the cost of the extra admission check.
func BenchmarkCompileServerResponseBodyEditInterleaved(b *testing.B) {
	caches := [2]*compileServerSessionCache{newCompileServerArtifactOnlyTestCache(4), newCompileServerSessionCache(4)}
	req := compileServerRequest{Mode: "obj", Filename: filepath.Join(b.TempDir(), "response-edits.elisa"), Incremental: true}
	fixture := strings.Replace(serverNativeArtifactSource(), "return value + 1 +", "return value + 0x%016x +", 1)
	req.Source = fmt.Sprintf(fixture, 1)
	requireServerNativeIdentity(b, req, caches[0])
	run := func(arm int) compileServerResponse {
		r, status := executeCompileServerRequestWithSessions(req, caches[arm])
		if status != http.StatusOK || !r.OK || r.Object == "" {
			b.Fatalf("arm %d failed: %d %+v", arm, status, r)
		}
		return r
	}
	for round := 0; round < 2; round++ {
		for arm := range caches {
			run(arm)
		}
	}
	type counters struct{ builds, emitted, reused, analyses, hits, responseHits uint64 }
	counts := func(arm int) counters {
		c := caches[arm]
		n, h := c.objects.Stats(), c.native.Stats()
		return counters{n.Builds, n.PartitionsEmitted, n.PartitionsReused, c.session(req.Filename, emitObject).Stats().Analyses, h.Hits, h.ResponseHits}
	}
	before := [2]counters{counts(0), counts(1)}
	var elapsed [2]time.Duration
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.Source = fmt.Sprintf(fixture, uint64(i)+2)
		var responses [2]compileServerResponse
		for turn := 0; turn < 2; turn++ {
			arm := (i + turn) % 2
			start := time.Now()
			responses[arm] = run(arm)
			elapsed[arm] += time.Since(start)
		}
		if responses[0] != responses[1] {
			b.Fatal("response admission changed edited object or diagnostics")
		}
	}
	b.StopTimer()
	for arm := range caches {
		after, old := counts(arm), before[arm]
		if after.builds-old.builds != uint64(b.N) || after.emitted-old.emitted != uint64(b.N) || after.reused-old.reused != uint64(7*b.N) || after.analyses-old.analyses != uint64(b.N) || after.hits != old.hits || after.responseHits != old.responseHits {
			b.Fatalf("arm %d failed to analyze/rebuild one shard per novel edit: before=%+v after=%+v", arm, old, after)
		}
	}
	b.ReportMetric(float64(elapsed[0])/float64(b.N), "artifact-ns/request")
	b.ReportMetric(float64(elapsed[1])/float64(b.N), "response-ns/request")
}
