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
)

func TestCompileServerIncrementalNativeObjects(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(4)
	handler := newCompileServerMuxWithSessions(nil, nil, cache)
	var source strings.Builder
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&source, "def work%d(value: i64) -> i64:\n    return value + %d + %s\n\n", i, i+1, strings.Repeat("(value * 2 - value * 2) + ", 15)+"(value * 2 - value * 2)")
	}
	source.WriteString("def main() -> i64:\n    return work0(20) + work31(1)\n")
	request := compileServerRequest{Mode: "obj", Filename: filepath.Join(t.TempDir(), "server_native.elisa"), Source: source.String()}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	native := func(response compileServerResponse, want int) {
		t.Helper()
		object, err := base64.StdEncoding.DecodeString(response.Object)
		if err != nil || len(object) == 0 {
			t.Fatalf("invalid object: %v", err)
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "main.o")
		exe := filepath.Join(dir, "main")
		if err := os.WriteFile(path, object, 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(clang, path, "-o", exe).CombinedOutput(); err != nil {
			t.Fatalf("native link: %v\n%s", err, out)
		}
		err = exec.Command(exe).Run()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != want {
			t.Fatalf("native status want %d: %v", want, err)
		}
	}
	cold, status := postCompileServerRequest(t, handler, request)
	if status != http.StatusOK || !cold.OK {
		t.Fatalf("fresh object: %+v", cold)
	}
	native(cold, 54)
	if cache.objects.Stats().Builds != 0 {
		t.Fatal("stateless request populated native cache")
	}
	request.Incremental = true
	for index := 0; index < 3; index++ {
		if index == 2 {
			request.Source = strings.Replace(request.Source, "return value + 1 +", "return value + 2 +", 1)
		}
		before := cache.objects.Stats()
		beforeNative := cache.native.Stats()
		response, status := postCompileServerRequest(t, handler, request)
		if status != http.StatusOK || !response.OK {
			t.Fatalf("retained object: %+v", response)
		}
		after := cache.objects.Stats()
		wantEmitted := uint64(8)
		wantStatus := 54
		if index == 1 {
			wantEmitted = 0
		}
		if index == 2 {
			wantEmitted = 1
			wantStatus = 55
		}
		wantReused := 8 - wantEmitted
		if index == 1 && cache.native.Stats().Hits-beforeNative.Hits == 1 {
			wantReused = 0
			if after.Builds != before.Builds || after.IRTime != before.IRTime {
				t.Fatal("completed-object hit entered LLVM")
			}
		}
		if after.PartitionsEmitted-before.PartitionsEmitted != wantEmitted || after.PartitionsReused-before.PartitionsReused != wantReused {
			t.Fatalf("wrong native reuse at revision%d: before=%+v after=%+v", index, before, after)
		}
		native(response, wantStatus)
	}
	before := cache.objects.Stats()
	request.Source = strings.Replace(request.Source, "value + 2", "missing + 2", 1)
	response, status := postCompileServerRequest(t, handler, request)
	if status != http.StatusBadRequest || response.OK || response.Object != "" {
		t.Fatalf("errorful source emitted stale native object: %+v", response)
	}
	if cache.objects.Stats().Builds != before.Builds {
		t.Fatal("failed semantic analysis reached native cache")
	}
}

func TestCompileServerAnonymousObjectsUseDirectEmission(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(4)
	handler := newCompileServerMuxWithSessions(nil, nil, cache)
	objects := make([]string, 2)
	dir := t.TempDir()
	for index, name := range []string{"left", "right"} {
		response, status := postCompileServerRequest(t, handler, compileServerRequest{Mode: "obj", Incremental: true, Source: fmt.Sprintf("def %s(value: i64) -> i64:\n    return value + %d\n", name, index+1)})
		if status != http.StatusOK || !response.OK {
			t.Fatalf("anonymous object failed: %+v", response)
		}
		object, err := base64.StdEncoding.DecodeString(response.Object)
		if err != nil {
			t.Fatal(err)
		}
		objects[index] = filepath.Join(dir, name+".o")
		if err := os.WriteFile(objects[index], object, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if cache.objects.Stats().Builds != 0 {
		t.Fatal("anonymous units entered filename-based partition namespace")
	}
	main := filepath.Join(dir, "main.c")
	if err := os.WriteFile(main, []byte("#include <stdint.h>\nextern int64_t left(int64_t), right(int64_t);\nint main(void) { return left(20) + right(10); }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "main")
	if out, err := exec.Command(clang, main, objects[0], objects[1], "-o", executable).CombinedOutput(); err != nil {
		t.Fatalf("anonymous units failed to link: %v\n%s", err, out)
	}
	err = exec.Command(executable).Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 33 {
		t.Fatalf("anonymous native status: %v", err)
	}
}

func TestCompileServerConcurrentNativeObjects(t *testing.T) {
	cache := newCompileServerArtifactOnlyTestCache(4)
	var source strings.Builder
	for i := 0; i < 32; i++ {
		fmt.Fprintf(&source, "def work%d(value: i64) -> i64:\n    return value + %d + %s\n\n", i, i+1, strings.Repeat("(value * 2 - value * 2) + ", 15)+"(value * 2 - value * 2)")
	}
	source.WriteString("def main() -> i64:\n    return work0(20) + work31(1)\n")
	requests := make([]compileServerRequest, 4)
	for i := range requests {
		requests[i] = compileServerRequest{Mode: "obj", Filename: fmt.Sprintf("concurrent-server%d.elisa", i), Source: source.String(), Incremental: true}
	}
	for round := 0; round < 2; round++ {
		before := cache.objects.Stats()
		beforeNative := cache.native.Stats()
		type result struct {
			response compileServerResponse
			status   int
		}
		replies := make(chan result, 4)
		for _, request := range requests {
			go func(request compileServerRequest) {
				response, status := executeCompileServerRequestWithSessions(request, cache)
				replies <- result{response, status}
			}(request)
		}
		for range requests {
			reply := <-replies
			if reply.status != http.StatusOK || !reply.response.OK {
				t.Fatalf("concurrent object failed: %+v", reply)
			}
			object, err := base64.StdEncoding.DecodeString(reply.response.Object)
			if err != nil || len(object) == 0 {
				t.Fatalf("bad concurrent object: %v", err)
			}
		}
		after := cache.objects.Stats()
		if round == 0 && after.PartitionsEmitted-before.PartitionsEmitted != 32 {
			t.Fatalf("concurrent cold admission: %+v", after)
		}
		completedHits := cache.native.Stats().Hits - beforeNative.Hits
		if completedHits > 4 {
			t.Fatal("invalid completed-hit count")
		}
		if round == 1 && (after.PartitionsEmitted != before.PartitionsEmitted || after.PartitionsReused-before.PartitionsReused != 8*(4-completedHits) || after.Builds-before.Builds != 4-completedHits) {
			t.Fatalf("concurrent warm reuse: %+v", after)
		}
	}
}
