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

func requireServerNativeIdentity(t *testing.T, request compileServerRequest, cache *compileServerSessionCache) {
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
