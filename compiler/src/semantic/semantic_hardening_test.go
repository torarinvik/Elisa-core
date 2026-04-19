package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func newSemanticHardeningTestAnalyzer() *Analyzer {
	return &Analyzer{
		file: &ast.File{Filename: "semantic_hardening.llcontext"},
		currentFuncDecl: &ast.FuncDecl{
			Position: lexer.Pos{File: "semantic_hardening.llcontext", Line: 1, Col: 1},
			Name:     "hardening_target",
		},
	}
}

func nestedOptionalType(depth int) Type {
	var current Type = &BuiltinType{Name: "i64"}
	for i := 0; i < depth; i++ {
		current = &OptionalType{Value: current}
	}
	return current
}

func requireSemanticHardeningError(t *testing.T, analyzer *Analyzer, want string) {
	t.Helper()
	if len(analyzer.diagnostics) == 0 {
		t.Fatalf("expected semantic hardening diagnostic containing %q", want)
	}
	got := analyzer.diagnostics[0].String()
	if !strings.Contains(got, want) {
		t.Fatalf("expected semantic hardening diagnostic containing %q, got %v", want, got)
	}
	if !strings.Contains(got, "hardening_target") {
		t.Fatalf("expected diagnostic to mention function context, got %v", got)
	}
}

func TestSubstituteTypeReportsRecursionLimit(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	result := analyzer.substituteType(nestedOptionalType(semanticSubstitutionDepthLimit+2), nil, nil, nil, nil)
	if !IsInvalidType(result) {
		t.Fatalf("expected invalidType after substitution hardening, got %T", result)
	}
	requireSemanticHardeningError(t, analyzer, "type substitution recursion limit")
}

func TestCloneTrackedValueTypeReportsRecursionLimit(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	result := analyzer.cloneTrackedValueType(nestedOptionalType(semanticCloneDepthLimit + 2))
	if !IsInvalidType(result) {
		t.Fatalf("expected invalidType after tracked-type clone hardening, got %T", result)
	}
	requireSemanticHardeningError(t, analyzer, "tracked-type clone recursion limit")
}

func TestContainsAffineHandleValuesReportsRecursionLimit(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	if analyzer.containsAffineHandleValues(nestedOptionalType(semanticTraversalDepthLimit+2), map[string]bool{}) {
		t.Fatal("expected affine traversal hardening to return false on deeply nested type")
	}
	requireSemanticHardeningError(t, analyzer, "affine-handle traversal recursion limit")
}

func TestContainsBorrowedOwnerRefValuesReportsRecursionLimit(t *testing.T) {
	analyzer := newSemanticHardeningTestAnalyzer()
	if analyzer.containsBorrowedOwnerRefValues(nestedOptionalType(semanticTraversalDepthLimit+2), map[string]bool{}) {
		t.Fatal("expected borrowed-owner traversal hardening to return false on deeply nested type")
	}
	requireSemanticHardeningError(t, analyzer, "borrowed-owner traversal recursion limit")
}
