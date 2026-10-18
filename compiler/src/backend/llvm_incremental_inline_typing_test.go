//go:build cgo

package backend

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestIncrementalTypingUsesCurrentLLVMInlineAttributes(t *testing.T) {
	session := semantic.NewIncrementalAnalysisSession()
	for revision, mode := range []string{"always", "never", "always"} {
		source := "@inline(" + mode + ")\ndef helper(value: int) -> int:\n    return value + 1\n"
		l := lexer.New("inline_reuse.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("inline_reuse.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("invalid inline fixture")
		}
		before := session.Stats()
		result := session.Analyze(file, semantic.AnalyzeOptions{})
		if len(result.Errors()) != 0 {
			t.Fatal(result.Errors())
		}
		if revision > 0 && session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
			t.Fatal("LLVM attribute test did not exercise cached typing")
		}
		g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
		if err != nil {
			t.Fatal(err)
		}
		output := g.printModule()
		g.dispose()
		attrs := functionAttributeGroupForTest(t, output, "helper")
		want, unwanted := "alwaysinline", "noinline"
		if mode == "never" {
			want, unwanted = unwanted, want
		}
		if !strings.Contains(attrs, want) || strings.Contains(attrs, unwanted) {
			t.Fatalf("mode %s emitted stale attributes %s", mode, attrs)
		}
	}
}
