//go:build cgo

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileServerResponseTracksBareFilenameExistence(t *testing.T) {
	t.Run("request_filename", func(t *testing.T) { testCompileServerResponsePathExistence(t, false, false) })
	t.Run("line_directive", func(t *testing.T) { testCompileServerResponsePathExistence(t, true, false) })
	t.Run("request_hard_error", func(t *testing.T) { testCompileServerResponsePathExistence(t, false, true) })
	t.Run("directive_hard_error", func(t *testing.T) { testCompileServerResponsePathExistence(t, true, true) })
}

func testCompileServerResponsePathExistence(t *testing.T, directive, reject bool) {
	f, err := os.CreateTemp(".", "response-path-state-*.elisa")
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(f.Name())
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(name) })
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	source := "def build(owner: Arena) -> void:\n    pass\n\ndef main() -> i64:\n    return 0\n"
	if reject {
		source = strings.ReplaceAll(source, "Arena", "StringView")
	}
	req := compileServerRequest{Mode: "obj", Filename: name, Source: source, Incremental: true}
	if directive {
		req.Filename = filepath.Join(t.TempDir(), "inline.elisa")
		req.Source = "#line 1 " + name + "\n" + source
	}
	cache := newCompileServerSessionCache(4)
	requireServerNativeIdentity(t, req, cache)
	before, status := executeCompileServerRequestWithSessions(req, cache)
	if status != http.StatusOK || !before.OK {
		t.Fatalf("initial: %d %+v", status, before)
	}
	assertReplay := func(want compileServerResponse) {
		t.Helper()
		hits := cache.native.Stats().ResponseHits
		got, status := executeCompileServerRequestWithSessions(req, cache)
		if status != http.StatusOK || got != want || cache.native.Stats().ResponseHits != hits+1 {
			t.Fatalf("unchanged path state failed exact replay: status=%d stderr=%q", status, got.Stderr)
		}
	}
	assertReplay(before)
	if strings.Contains(before.Stderr, "internal runtime carrier") {
		t.Fatal("missing-path fixture unexpectedly warns")
	}
	if err := os.WriteFile(name, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	retained, status := executeCompileServerRequestWithSessions(req, cache)
	fresh, freshStatus := executeCompileServerRequestWithSessions(req, newCompileServerSessionCache(4))
	if !strings.Contains(fresh.Stderr, "internal runtime carrier") {
		t.Fatalf("fresh fixture did not exercise path-sensitive diagnostic: %+v", fresh)
	}
	if status != freshStatus || retained != fresh {
		t.Fatalf("file creation changed diagnostics without invalidating response: retained stderr=%q fresh stderr=%q", retained.Stderr, fresh.Stderr)
	}
	if reject {
		if status != http.StatusBadRequest || retained.OK || retained.Object != "" {
			t.Fatalf("path creation did not reject carrier: status=%d stderr=%q", status, retained.Stderr)
		}
		hits := cache.native.Stats().ResponseHits
		repeated, repeatedStatus := executeCompileServerRequestWithSessions(req, cache)
		if repeatedStatus != status || repeated != retained || cache.native.Stats().ResponseHits != hits {
			t.Fatal("failed path state was cached or changed diagnostics")
		}
	} else {
		assertReplay(retained)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	recovered, recoveredStatus := executeCompileServerRequestWithSessions(req, cache)
	if recoveredStatus != http.StatusOK || recovered != before {
		t.Fatalf("removed path did not restore original response: stderr=%q", recovered.Stderr)
	}
	assertReplay(before)

}

func BenchmarkServerDiagnosticPathInputs(b *testing.B) {
	source := serverNativeArtifactSource()
	for _, directive := range []bool{false, true} {
		name := "ordinary"
		text := source
		if directive {
			name = "attributed"
			text = "#line 1 synthetic.elisa\n" + source
		}
		b.Run(name, func(b *testing.B) {
			data := []byte(text)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = diagnosticPathInputs("/virtual/unit.elisa", data)
			}
		})
	}
}
