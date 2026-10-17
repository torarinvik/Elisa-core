//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

// This qualifies the scalar metadata adapter in codegen, not a body-cache hit:
// all other metadata and live-analyzer state still come from fresh analysis.
func TestGenerateLLVMIRWithRestoredScalarObservations(t *testing.T) {
	const source = `def work(value: i64) -> i64:
    local = value + 2
    return local * 3
`
	analyze := func(source string) (*semantic.Result, *semantic.FunctionSourceIndex) {
		t.Helper()
		l := lexer.New("scalar_replay.elisa", []byte(source))
		tokens := l.Tokenize()
		if len(l.Errors()) != 0 {
			t.Fatal(l.Errors())
		}
		p := parser.New(tokens)
		file := p.ParseFile("scalar_replay.elisa")
		if len(p.Errors()) != 0 {
			t.Fatal(p.Errors())
		}
		index, err := semantic.IndexFunctionSource(file.Decls[0].(*ast.FuncDecl), semantic.FunctionIdentityContext{Origin: "/project/scalar_replay.elisa", QualifiedName: "work"})
		if err != nil {
			t.Fatal(err)
		}
		result := semantic.Analyze(file)
		if len(result.Errors()) != 0 {
			t.Fatal(result.Errors())
		}
		return result, index
	}
	previous, index := analyze(source)
	facts, err := semantic.CaptureFunctionScalarFacts(index, semantic.FunctionScalarObservations{
		ExprTypes: previous.ExprTypes, ResolvedTypeNames: previous.ResolvedTypeNames, ResolvedValueNames: previous.ResolvedValueNames,
		PermGrowthOps: previous.PermGrowthOps, RewriteDefaults: previous.RewriteDefaults, IndexBoundsProven: previous.IndexBoundsProven,
		LemmaCalls: previous.LemmaCalls, GhostDecls: previous.GhostDecls, GhostContracts: previous.GhostContracts,
	})
	if err != nil {
		t.Fatal(err)
	}
	currentSource := "\n\n" + source
	cold, _ := analyze(currentSource)
	want, err := generateLLVMIRWithDefaultPackedLoweringForTest(cold)
	if err != nil {
		t.Fatal(err)
	}
	current, target := analyze(currentSource)
	restored, err := facts.Restore(target)
	if err != nil {
		t.Fatal(err)
	}
	// Replace covered outputs completely so fresh analysis cannot hide an omitted
	// observation in the adapter. The other Result fields remain current.
	current.ExprTypes = restored.ExprTypes
	current.ResolvedTypeNames = restored.ResolvedTypeNames
	current.ResolvedValueNames = restored.ResolvedValueNames
	current.PermGrowthOps = restored.PermGrowthOps
	current.RewriteDefaults = restored.RewriteDefaults
	current.IndexBoundsProven = restored.IndexBoundsProven
	current.LemmaCalls = restored.LemmaCalls
	current.GhostDecls = restored.GhostDecls
	current.GhostContracts = restored.GhostContracts
	got, err := generateLLVMIRWithDefaultPackedLoweringForTest(current)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("restored observations changed LLVM IR:\n%s\nwant:\n%s", got, want)
	}
}
