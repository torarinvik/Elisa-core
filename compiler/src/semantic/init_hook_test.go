package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeTypeNamedConstructorDeclSugar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "init_hook_named_constructor.elisa", `struct Span:
    start: i64
    finish: i64

def Span(start: i64) -> Span:
    return Span{start, finish: start + 1}

def build(start: i64) -> i64:
    inferred: Span = Span(start:)
    return inferred.finish
`)
	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok || buildSym == nil {
		t.Fatal("expected build symbol")
	}
	buildDecl, ok := buildSym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected build func decl, got %T", buildSym.Node)
	}
	inferredDecl, ok := buildDecl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected inferred var decl, got %T", buildDecl.Body[0])
	}
	inferredLit, ok := inferredDecl.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected inferred constructor expr, got %T", inferredDecl.Value)
	}
	inferredCall := result.InitCalls[inferredLit]
	if inferredCall == nil {
		t.Fatal("expected type-named constructor declaration to lower through __init__")
	}
	callee, ok := inferredCall.Func.(*ast.Ident)
	if !ok || callee.Name == "Span" {
		t.Fatalf("expected lowered constructor call to target hidden init symbol, got %T %#v", inferredCall.Func, inferredCall.Func)
	}
	if len(inferredCall.LoweredArgs()) != 1 {
		t.Fatalf("expected one lowered arg for type-named constructor sugar, got %d", len(inferredCall.LoweredArgs()))
	}
}

// A custom `def Type(...)` constructor COEXISTS with the implicit all-fields
// positional form instead of shadowing it: defining `def P()` must not break
// `P(a, b)`. (Previously `P(3, 4)` reported "no matching __init__ overload".)
func TestAnalyzePositionalConstructionCoexistsWithCustomConstructor(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ctor_coexist.elisa", `struct P:
    x: i64
    y: i64

def P() -> P:
    return P{x: 0, y: 0}

def build() -> i64:
    a: P = P()
    b: P = P(3, 4)
    return a.x + b.x + b.y
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzePascalCaseFunctionCallFallsBackBeforeStructDiagnostic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "pascal_case_function_call.elisa", `def MakeSpan() -> i64:
    return 7

def build() -> i64:
    return MakeSpan()
`)
	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok || buildSym == nil {
		t.Fatal("expected build symbol")
	}
	if len(result.Errors()) != 0 {
		t.Fatalf("expected PascalCase function call to analyze without unknown struct diagnostic, got %v", result.Errors())
	}
}
