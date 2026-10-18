//go:build cgo

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileServerSuffixReplayUsesCurrentDefaultArguments(t *testing.T) {
	source := "def first(x: i64 = 41) -> i64:\n    return x + 1\n\ndef main() -> i64:\n    return first()\n"
	cache := newCompileServerArtifactOnlyTestCache(2)
	request := compileServerRequest{Filename: "suffix_defaults.elisa", Source: source, Incremental: true}
	for _, mode := range []string{"semantic", "lowered", "llvm"} {
		request.Mode = mode
		for i := 0; i < 2; i++ {
			got, _ := executeCompileServerRequestWithSessions(request, cache)
			if !got.OK {
				t.Fatal(got)
			}
		}
	}
	before := cache.tokens.suffixParsedDeclarations
	for _, value := range []string{"42", "43", "41"} {
		request.Source = strings.Replace(source, "= 41", "= "+value, 1)
		for _, mode := range []string{"semantic", "lowered", "llvm"} {
			request.Mode = mode
			got, status := executeCompileServerRequestWithSessions(request, cache)
			cold := request
			cold.Incremental = false
			want, wantStatus := executeCompileServerRequest(cold)
			if status != wantStatus || got != want || !got.OK {
				t.Fatalf("mode=%s default=%s stale after lowering: %#v / %#v", mode, value, got, want)
			}
		}
	}
	if cache.tokens.suffixParsedDeclarations == before {
		t.Fatal("no suffix replay exercised")
	}
}

func TestCompileServerSuffixReplayNativeEdits(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("native qualification requires clang")
	}
	var text strings.Builder
	text.WriteString("def first(x: i64 = 41) -> i64:\n    return x + 1\n\n")
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&text, "def work%d(x: i64) -> i64:\n    return %s\n\n", i, strings.Repeat("x * 2 + ", 31)+"x * 2")
	}
	text.WriteString("def main() -> i64:\n    return first()\n")
	source := text.String()
	cache := newCompileServerArtifactOnlyTestCache(2)
	request := compileServerRequest{Mode: "obj", Filename: "suffix_native.elisa", Source: source, Incremental: true}
	for i := 0; i < 2; i++ {
		got, _ := executeCompileServerRequestWithSessions(request, cache)
		if !got.OK {
			t.Fatal(got)
		}
	}
	dir := t.TempDir()
	for _, value := range []int{42, 43, 41} {
		request.Source = strings.Replace(source, "= 41", fmt.Sprintf("= %d", value), 1)
		for _, retained := range []bool{false, true} {
			current := request
			current.Incremental = retained
			response, _ := executeCompileServerRequestWithSessions(current, cache)
			if !response.OK {
				t.Fatal(response)
			}
			object, err := base64.StdEncoding.DecodeString(response.Object)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, fmt.Sprintf("edit-%d-%t", value, retained))
			if err := os.WriteFile(path+".o", object, 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(clang, path+".o", "-o", path).CombinedOutput(); err != nil {
				t.Fatalf("link: %v %s", err, output)
			}
			err = exec.Command(path).Run()
			status := 0
			if exit, ok := err.(*exec.ExitError); ok {
				status = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if status != value+1 {
				t.Fatalf("retained=%t edit=%d returned %d", retained, value, status)
			}
		}
	}
	if cache.tokens.suffixParsedDeclarations < 65 {
		t.Fatal("native test did not replay a suffix")
	}
}
