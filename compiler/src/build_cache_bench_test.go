//go:build cgo

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Measures the disk cache executor, including source loading, identity checks,
// transcript validation and checked output restore. Warm builds must bypass the
// compiler callback; command-line argument parsing is outside the timed region.
func BenchmarkBuildObjectCacheWarmExecutor(b *testing.B) {
	for _, functions := range []int{16, 128} {
		b.Run(fmt.Sprint(functions), func(b *testing.B) {
			dir := b.TempDir()
			b.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
			b.Setenv("ELISACORE_BUILD_CACHE", "1")
			var text strings.Builder
			for i := 0; i < functions; i++ {
				fmt.Fprintf(&text, "def work%d(value: i64) -> i64:\n    return value + %d\n\n", i, i)
			}
			text.WriteString("def main() -> i64:\n    return work0(7)\n")
			source := filepath.Join(dir, "unit.elisa")
			if err := os.WriteFile(source, []byte(text.String()), 0600); err != nil {
				b.Fatal(err)
			}
			options, err := parseArgs([]string{"-emit", "obj", "-o", filepath.Join(dir, "result.o"), source})
			if err != nil {
				b.Fatal(err)
			}
			if rc, handled := tryBuildObjectCacheWith(options, io.Discard, io.Discard, loadProgramInput, runLoadedProgramWithOptions); !handled || rc != 0 {
				b.Fatalf("cold cache seed failed: rc=%d handled=%v", rc, handled)
			}
			noBuild := func(cliOptions, *loadedProgram, io.Writer, io.Writer) int {
				b.Fatal("warm cache entered compiler")
				return 1
			}
			b.ReportAllocs()
			b.SetBytes(int64(text.Len()))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if rc, handled := tryBuildObjectCacheWith(options, io.Discard, io.Discard, loadProgramInput, noBuild); !handled || rc != 0 {
					b.Fatalf("warm cache failed: rc=%d handled=%v", rc, handled)
				}
			}
		})
	}
}
