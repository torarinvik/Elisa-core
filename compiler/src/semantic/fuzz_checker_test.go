package semantic

import (
	"strings"
	"testing"
	"time"

	"elisacore/internal/fuzzseed"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func fuzzParseClean(src []byte) *ast.File {
	l := lexer.New("fuzz.elisa", src)
	toks := l.Tokenize()
	if len(l.Errors()) != 0 {
		return nil
	}
	p := parser.New(toks)
	file := p.ParseFile("fuzz.elisa")
	if len(p.Errors()) != 0 {
		return nil
	}
	return file
}

func fuzzAnalyzeText(src []byte) string {
	file := fuzzParseClean(src)
	if file == nil {
		return ""
	}
	r := AnalyzeWithOptions(file, AnalyzeOptions{})
	return strings.Join(r.Errors(), "\n") + "\n--notices--\n" + strings.Join(r.Notices(), "\n")
}

// FuzzChecker: semantic analysis of any program that parses must terminate, never panic,
// and be deterministic. The analysis is run twice on fresh parses (analysis lowers the
// AST in place); Go randomises map iteration per range statement, so two runs in one
// process expose order-dependent diagnostics the same way two processes would.
func FuzzChecker(f *testing.F) {
	for _, s := range fuzzseed.Seeds() {
		f.Add(s)
	}
	fuzzseed.LimitStack()
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > fuzzseed.MaxInput {
			return
		}
		if fuzzParseClean(src) == nil {
			return
		}
		stop := fuzzseed.Watchdog(60*time.Second, "checker", src)
		defer stop()
		first := fuzzAnalyzeText(src)
		second := fuzzAnalyzeText(src)
		if first != second {
			t.Fatalf("nondeterministic diagnostics:\n%s\n=== vs ===\n%s", first, second)
		}
	})
}
