package lexer_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"llcontext/src/lexer"
)

func repoRootFromLexerBench(b *testing.B) string {
	b.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("failed to determine benchmark file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

func BenchmarkTokenizeSelfHostedFrontendSource(b *testing.B) {
	repoRoot := repoRootFromLexerBench(b)
	sourcePath := filepath.Join(repoRoot, "Code", "frontend_llcontext", "frontend_lexer.llcontext")
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		b.Fatalf("failed to read %s: %v", sourcePath, err)
	}

	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := lexer.New(sourcePath, raw)
		tokens := l.Tokenize()
		if len(tokens) == 0 {
			b.Fatal("expected at least one token")
		}
	}
}
