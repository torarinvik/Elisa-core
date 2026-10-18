//go:build cgo

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileServerLexerConvergenceNativeShiftedEdits(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("native qualification requires clang")
	}
	var code strings.Builder
	code.WriteString("def first() -> i64:\n    return 41\n\n")
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&code, "def work%d(x: i64) -> i64:\n    return %s\n\n", i, strings.Repeat("x * 2 + ", 31)+"x * 2")
	}
	code.WriteString("def main() -> i64:\n    return first()\n")
	source := code.String()
	cache := newCompileServerSessionCache(2)
	request := compileServerRequest{Mode: "obj", Filename: "lexer_convergence_native.elisa", Source: source, Incremental: true}
	for i := 0; i < 2; i++ {
		got, _ := executeCompileServerRequestWithSessions(request, cache)
		if !got.OK {
			t.Fatal(got)
		}
	}
	dir := t.TempDir()
	edits := []struct {
		body   string
		status int
	}{{"142", 142}, {"10 + 20\n\n", 30}, {"41", 41}}
	for revision, edit := range edits {
		request.Source = strings.Replace(source, "return 41", "return "+edit.body, 1)
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
			path := filepath.Join(dir, fmt.Sprintf("edit-%d-%t", revision, retained))
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
			if status != edit.status {
				t.Fatalf("revision=%d retained=%t got %d want %d", revision, retained, status, edit.status)
			}
		}
		key := sourceTokenKey{filename: request.Filename, source: sha256.Sum256([]byte(request.Source))}
		cache.tokens.mu.Lock()
		entry, ok := cache.tokens.entries[key]
		cache.tokens.mu.Unlock()
		if !ok || entry.lexed.SuffixReusedTokens() == 0 {
			t.Fatal("native history did not exercise lexer convergence")
		}
	}
}
