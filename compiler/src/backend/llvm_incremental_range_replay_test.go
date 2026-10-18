//go:build cgo

package backend

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"path/filepath"
	"strings"
	"testing"
)

func TestIncrementalRangePredicateNativeParity(t *testing.T) {
	base := "def inside(value: int) -> bool:\n    return value in 1 ..= 5\n\ndef main() -> int:\n    return 42 if inside(5) else 1\n"
	session := semantic.NewIncrementalAnalysisSession()
	for revision, source := range []string{base, base, strings.Replace(base, "..=", "..<", 1), strings.Replace(base, "..=", "..<", 1)} {
		l := lexer.New("range_reuse.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("range_reuse.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("invalid range native fixture")
		}
		before := session.Stats()
		result := session.Analyze(file, semantic.AnalyzeOptions{})
		if len(result.Errors()) != 0 {
			t.Fatal(result.Errors())
		}
		if (revision == 1 || revision == 3) && session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
			t.Fatal("native fixture did not exercise cached range typing")
		}
		object := filepath.Join(t.TempDir(), "range.o")
		if err := WriteLLVMObjectFileWithOptions(result, object, LLVMObjectEmitOptions{Serial: true}); err != nil {
			t.Fatal(err)
		}
		want := 42
		if revision >= 2 {
			want = 1
		}
		if got := incrementalObjectNativeStatus(t, object); got != want {
			t.Fatalf("revision %d native status %d, want %d", revision, got, want)
		}
	}
}
