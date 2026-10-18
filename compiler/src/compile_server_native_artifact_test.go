//go:build cgo

package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/backend"
)

func serverNativeArtifactSource() string {
	var source strings.Builder
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&source, "def work%d(value: i64) -> i64:\n    return value + %d + %s\n\n", i, i+1, strings.Repeat("(value * 2 - value * 2) + ", 15)+"(value * 2 - value * 2)")
	}
	source.WriteString("def main() -> i64:\n    return work0(20) + work31(1)\n")
	return source.String()
}

func requireServerNativeIdentity(t testing.TB, request compileServerRequest, cache *compileServerSessionCache) {
	t.Helper()
	program, err := loadedProgramFromRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := serverNativeObjectIdentity(request, program, cache); !ok {
		var names []string
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			if !serverNativeEnvironmentAllowed([]string{entry}) {
				names = append(names, name)
			}
		}
		selected, selectedErr := exec.LookPath("ld")
		_, compilerErr := compilerSourceStamp()
		artifact, artifactOK := buildCacheObjectArtifactForProgram(cliOptions{emit: emitObject, filename: program.filename, optLevel: backend.OptimizationLevel0, hasOptLevel: true}, program)
		_ = artifact
		_, tool, toolErr := cache.objects.ObjectMergerCacheInputs()
		t.Logf("decline diagnostic: env names=%v selected=%s err=%v compiler=%v artifact=%v merger=%s err=%v", names, selected, selectedErr, compilerErr, artifactOK, tool, toolErr)
		t.Skip("whole-object memo requires default compiler configuration and system merger")
	}
}

func TestCompileServerNativeArtifactKeepsAnalysisAndSkipsLLVM(t *testing.T) {
	cache := newCompileServerSessionCache(4)
	request := compileServerRequest{Mode: "obj", Filename: filepath.Join(t.TempDir(), "native_noop.elisa"), Source: serverNativeArtifactSource(), Incremental: true}
	requireServerNativeIdentity(t, request, cache)
	run := func() compileServerResponse {
		t.Helper()
		response, status := executeCompileServerRequestWithSessions(request, cache)
		if status != http.StatusOK || !response.OK {
			t.Fatalf("compile: status%d %+v", status, response)
		}
		return response
	}
	first := run()
	before := cache.objects.Stats()
	semanticBefore := cache.session(request.Filename, emitObject).Stats()
	warm := run()
	after := cache.objects.Stats()
	semanticAfter := cache.session(request.Filename, emitObject).Stats()
	if warm.Object != first.Object || warm.Stderr != first.Stderr || cache.native.Stats().Hits != 1 {
		t.Fatal("no-op did not return the exact completed artifact")
	}
	if semanticAfter.Analyses != semanticBefore.Analyses+1 {
		t.Fatal("artifact reuse skipped semantic validation")
	}
	if after.Builds != before.Builds || after.IRTime != before.IRTime || after.PartitionsReused != before.PartitionsReused {
		t.Fatal("artifact hit reached LLVM/object pipeline")
	}
	// A cached object must not hide new semantic errors.
	valid := request.Source
	request.Source = strings.Replace(valid, "return value + 1 +", "return missing + 1 +", 1)
	bad, status := executeCompileServerRequestWithSessions(request, cache)
	if status != http.StatusBadRequest || bad.OK || bad.Object != "" || !strings.Contains(bad.Stderr, "missing") {
		t.Fatalf("stale artifact hid errors: %+v", bad)
	}
	if cache.native.Stats().Hits != 1 || cache.objects.Stats().Builds != after.Builds {
		t.Fatal("errorful request reached output cache/backend")
	}
	request.Source = strings.Replace(valid, "return value + 1 +", "return value + 2 +", 1)
	edited := run()
	if cache.objects.Stats().Builds != after.Builds+1 || cache.native.Stats().Hits != 1 {
		t.Fatal("body edit reused whole object")
	}
	object, err := base64.StdEncoding.DecodeString(edited.Object)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.o")
	exe := filepath.Join(dir, "out")
	if err = os.WriteFile(path, object, 0600); err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang, path, "-o", exe).CombinedOutput(); err != nil {
		t.Fatalf("link: %v %s", err, out)
	}
	err = exec.Command(exe).Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 55 {
		t.Fatalf("edited artifact returned wrong value: %v", err)
	}
}

func TestCompileServerNativeArtifactWarningsAndDeclines(t *testing.T) {
	t.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", "0")
	cache := newCompileServerSessionCache(4)
	request := compileServerRequest{Mode: "obj", Filename: filepath.Join(t.TempDir(), "warnings.elisa"), Source: "@deprecated(\"use newer\")\ndef old() -> i64:\n    return 3\n\ndef main() -> i64:\n    return old()\n", Incremental: true}
	requireServerNativeIdentity(t, request, cache)
	first, status := executeCompileServerRequestWithSessions(request, cache)
	if status != http.StatusOK || !first.OK || !strings.Contains(first.Stderr, "use newer") {
		t.Fatalf("cold warning missing: %+v", first)
	}
	warm, status := executeCompileServerRequestWithSessions(request, cache)
	if status != http.StatusOK || warm.Stderr != first.Stderr || cache.native.Stats().Hits != 1 {
		t.Fatal("artifact hit changed warnings")
	}
	before := cache.objects.Stats()
	t.Setenv("ELISA_UNKNOWN_FUTURE_SWITCH", "1")
	fresh, status := executeCompileServerRequestWithSessions(request, cache)
	if status != http.StatusOK || !fresh.OK || cache.objects.Stats().Builds != before.Builds+1 || cache.native.Stats().Hits != 1 {
		t.Fatal("unknown configuration did not decline reuse")
	}
}

func TestCompileServerNativeArtifactOwnerAndPayloadDeclines(t *testing.T) {
	cache := newCompileServerSessionCache(4)
	request := compileServerRequest{Mode: "obj", Filename: "identity.elisa", Source: "def main() -> i64:\n    return 1\n", Incremental: true}
	program, err := loadedProgramFromRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	requireServerNativeIdentity(t, request, cache)
	first, ok := serverNativeObjectIdentity(request, program, cache)
	if !ok {
		t.Fatal("base identity")
	}
	const secret = "native-cache-credential-must-not-be-retained-20261018"
	t.Setenv("NATIVE_CACHE_TEST_CREDENTIAL", secret)
	withCredential, ok := serverNativeObjectIdentity(request, program, cache)
	if !ok || withCredential.config == first.config || strings.Contains(withCredential.config, secret) {
		t.Fatal("environment identity missing or retains credential text")
	}
	t.Setenv("NATIVE_CACHE_TEST_CREDENTIAL", secret+"-changed")
	changedCredential, ok := serverNativeObjectIdentity(request, program, cache)
	if !ok || changedCredential.config == withCredential.config {
		t.Fatal("environment change did not invalidate identity")
	}
	// Compare filename changes under the same current environment.
	first = changedCredential
	otherRequest := request
	otherRequest.Filename = "different.elisa"
	otherProgram, err := loadedProgramFromRequest(otherRequest)
	if err != nil {
		t.Fatal(err)
	}
	other, ok := serverNativeObjectIdentity(otherRequest, otherProgram, cache)
	if !ok || other.config == first.config || other.digest == first.digest {
		t.Fatal("unit identity ignored")
	}
	for _, mutate := range []func(){func() { program.file = &ast.File{} }, func() { program.source = []byte("different") }, func() { request.Filename = "" }, func() { request.Incremental = false },
		func() { request.IR = "injected" },
		func() { program.sourceFiles = []string{"external-authority"} },
		func() { cache.objects = nil; cache.native.owner = nil }, func() { cache.objects = backend.NewIncrementalObjectSession(4, 0) }} {
		savedRequest := request
		savedProgram := *program
		savedObjects := cache.objects
		savedOwner := cache.native.owner
		mutate()
		if _, ok := serverNativeObjectIdentity(request, program, cache); ok {
			t.Fatal("unattested input accepted")
		}
		request = savedRequest
		*program = savedProgram
		cache.objects = savedObjects
		cache.native.owner = savedOwner
	}
}

func TestCompileServerNativeArtifactConcurrentSameIdentity(t *testing.T) {
	cache := newCompileServerSessionCache(4)
	request := compileServerRequest{Mode: "obj", Filename: filepath.Join(t.TempDir(), "same.elisa"), Source: serverNativeArtifactSource(), Incremental: true}
	requireServerNativeIdentity(t, request, cache)
	first, status := executeCompileServerRequestWithSessions(request, cache)
	if status != http.StatusOK || !first.OK {
		t.Fatalf("warmup: %+v", first)
	}
	before := cache.objects.Stats()
	hits := cache.native.Stats().Hits
	analyses := cache.session(request.Filename, emitObject).Stats().Analyses
	type result struct {
		response compileServerResponse
		status   int
	}
	replies := make(chan result, 4)
	for i := 0; i < 4; i++ {
		go func() {
			response, status := executeCompileServerRequestWithSessions(request, cache)
			replies <- result{response, status}
		}()
	}
	for i := 0; i < 4; i++ {
		reply := <-replies
		if reply.status != http.StatusOK || !reply.response.OK || reply.response.Object != first.Object || reply.response.Stderr != first.Stderr {
			t.Errorf("concurrent artifact mismatch: status=%d error=%s", reply.status, reply.response.Error)
		}
	}
	after := cache.objects.Stats()
	if after.Builds != before.Builds || after.IRTime != before.IRTime || after.PartitionsReused != before.PartitionsReused {
		t.Fatal("concurrent hit entered native build")
	}
	if cache.native.Stats().Hits != hits+4 || cache.session(request.Filename, emitObject).Stats().Analyses != analyses+4 {
		t.Fatal("concurrent reuse skipped validation or missed artifact")
	}
}

// Both arms run the complete request path, including fresh analysis and key
// preparation. Only completed-object memoization differs; shard reuse stays on.
func BenchmarkCompileServerNativeNoop(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "shards"
		if enabled {
			name = "completed"
		}
		b.Run(name, func(b *testing.B) {
			cache := newCompileServerSessionCache(4)
			request := compileServerRequest{Mode: "obj", Filename: filepath.Join(b.TempDir(), "fixed.elisa"), Source: serverNativeArtifactSource(), Incremental: true}
			requireServerNativeIdentity(b, request, cache)
			if !enabled {
				cache.native = nil
			}
			var expected compileServerResponse
			for i := 0; i < 2; i++ {
				response, status := executeCompileServerRequestWithSessions(request, cache)
				if status != http.StatusOK || !response.OK {
					b.Fatalf("warmup: %+v", response)
				}
				expected = response
			}
			before := cache.objects.Stats()
			analyses := cache.session(request.Filename, emitObject).Stats().Analyses
			var hits uint64
			if enabled {
				hits = cache.native.Stats().Hits
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response, status := executeCompileServerRequestWithSessions(request, cache)
				if status != http.StatusOK || !response.OK || response.Object != expected.Object || response.Stderr != expected.Stderr {
					b.Fatal("request changed object or diagnostics")
				}
			}
			b.StopTimer()
			after := cache.objects.Stats()
			if cache.session(request.Filename, emitObject).Stats().Analyses != analyses+uint64(b.N) {
				b.Fatal("analysis skipped")
			}
			if enabled {
				if cache.native.Stats().Hits != hits+uint64(b.N) || after.Builds != before.Builds || after.IRTime != before.IRTime {
					b.Fatal("completed object was not reused")
				}
			} else if after.Builds-before.Builds != uint64(b.N) || after.PartitionsReused-before.PartitionsReused != uint64(8*b.N) || after.PartitionsEmitted != before.PartitionsEmitted {
				b.Fatal("baseline did not reuse native shards")
			}
			b.ReportMetric(float64(after.IRTime-before.IRTime)/float64(b.N), "IR-ns/op")
			b.ReportMetric(float64(after.NativeTime-before.NativeTime)/float64(b.N), "native-ns/op")
		})
	}
}

// Novel fixed-width edits must continue through native shard reuse. This control
// measures the completed-cache lookup/publication cost when no whole object hits.
func BenchmarkCompileServerNativeBodyEdit(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "shards"
		if enabled {
			name = "completed"
		}
		b.Run(name, func(b *testing.B) {
			cache := newCompileServerSessionCache(4)
			request := compileServerRequest{Mode: "obj", Filename: filepath.Join(b.TempDir(), "edits.elisa"), Incremental: true}
			fixture := strings.Replace(serverNativeArtifactSource(), "return value + 1 +", "return value + 0x%016x +", 1)
			request.Source = fmt.Sprintf(fixture, 1)
			requireServerNativeIdentity(b, request, cache)
			if !enabled {
				cache.native = nil
			}
			previous := ""
			for i := 0; i < 2; i++ {
				response, status := executeCompileServerRequestWithSessions(request, cache)
				if status != http.StatusOK || !response.OK {
					b.Fatalf("warmup: %+v", response)
				}
				previous = response.Object
			}
			before := cache.objects.Stats()
			analyses := cache.session(request.Filename, emitObject).Stats().Analyses
			var hits uint64
			if enabled {
				hits = cache.native.Stats().Hits
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				request.Source = fmt.Sprintf(fixture, uint64(i)+2)
				response, status := executeCompileServerRequestWithSessions(request, cache)
				if status != http.StatusOK || !response.OK || response.Object == "" || response.Object == previous {
					b.Fatal("edit failed to produce a changed object")
				}
				previous = response.Object
			}
			b.StopTimer()
			after := cache.objects.Stats()
			if cache.session(request.Filename, emitObject).Stats().Analyses != analyses+uint64(b.N) || after.Builds-before.Builds != uint64(b.N) || after.PartitionsEmitted-before.PartitionsEmitted != uint64(b.N) || after.PartitionsReused-before.PartitionsReused != uint64(7*b.N) {
				b.Fatal("novel edits did not rebuild exactly one shard per request")
			}
			if enabled && cache.native.Stats().Hits != hits {
				b.Fatal("novel edit hit whole-object cache")
			}
			b.ReportMetric(float64(after.IRTime-before.IRTime)/float64(b.N), "IR-ns/op")
		})
	}
}

// Isolate identity preparation from parsing, analysis, LLVM and object I/O.
// The ordinary miss path currently computes this identity twice.
func BenchmarkCompileServerNativeIdentity(b *testing.B) {
	cache := newCompileServerSessionCache(4)
	request := compileServerRequest{Mode: "obj", Filename: filepath.Join(b.TempDir(), "identity.elisa"), Source: serverNativeArtifactSource(), Incremental: true}
	requireServerNativeIdentity(b, request, cache)
	program, err := loadedProgramFromRequest(request)
	if err != nil {
		b.Fatal(err)
	}
	first, ok := serverNativeObjectIdentity(request, program, cache)
	if !ok {
		b.Fatal("identity declined")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		identity, ok := serverNativeObjectIdentity(request, program, cache)
		if !ok || identity.config != first.config || identity.source != first.source {
			b.Fatal("identity changed")
		}
	}
}

// Interleaving controls process initialization and short-term host drift. It
// shares process-wide LLVM/GC state, so it complements independent-process arms.
func BenchmarkCompileServerNativeBodyEditInterleaved(b *testing.B) {
	caches := [2]*compileServerSessionCache{newCompileServerSessionCache(4), newCompileServerSessionCache(4)}
	request := compileServerRequest{Mode: "obj", Filename: filepath.Join(b.TempDir(), "paired.elisa"), Incremental: true}
	fixture := strings.Replace(serverNativeArtifactSource(), "return value + 1 +", "return value + 0x%016x +", 1)
	request.Source = fmt.Sprintf(fixture, 1)
	requireServerNativeIdentity(b, request, caches[1])
	caches[0].native = nil
	run := func(arm int) compileServerResponse {
		response, status := executeCompileServerRequestWithSessions(request, caches[arm])
		if status != http.StatusOK || !response.OK || response.Object == "" {
			b.Fatalf("arm %d failed: %+v", arm, response)
		}
		return response
	}
	for round := 0; round < 2; round++ {
		for arm := range caches {
			run(arm)
		}
	}
	before := [2]backend.IncrementalObjectStats{caches[0].objects.Stats(), caches[1].objects.Stats()}
	analyses := [2]uint64{caches[0].session(request.Filename, emitObject).Stats().Analyses, caches[1].session(request.Filename, emitObject).Stats().Analyses}
	hits := caches[1].native.Stats().Hits
	var elapsed [2]time.Duration
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		request.Source = fmt.Sprintf(fixture, uint64(i)+2)
		var output [2]compileServerResponse
		for turn := 0; turn < 2; turn++ {
			arm := (i + turn) % 2
			started := time.Now()
			output[arm] = run(arm)
			elapsed[arm] += time.Since(started)
		}
		if output[0].Object != output[1].Object || output[0].Stderr != output[1].Stderr {
			b.Fatal("paired edit changed object or diagnostics")
		}
	}
	b.StopTimer()
	for arm, cache := range caches {
		after := cache.objects.Stats()
		if after.Builds-before[arm].Builds != uint64(b.N) || after.PartitionsEmitted-before[arm].PartitionsEmitted != uint64(b.N) || after.PartitionsReused-before[arm].PartitionsReused != uint64(7*b.N) || cache.session(request.Filename, emitObject).Stats().Analyses != analyses[arm]+uint64(b.N) {
			b.Fatal("paired edit bypassed analysis or native shard rebuild")
		}
	}
	if caches[1].native.Stats().Hits != hits {
		b.Fatal("novel edit reused whole object")
	}
	b.ReportMetric(float64(elapsed[0])/float64(b.N), "shards-ns/request")
	b.ReportMetric(float64(elapsed[1])/float64(b.N), "completed-ns/request")
}
