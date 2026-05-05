package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeInitHookOverloadsForParenStructConstructors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "init_hook_overloads.elisa", `struct Span:
    start: i64
    finish: i64

def __init__(start: i64) -> Span:
    return Span{start, finish: start + 1}

def __init__(start: i64, finish: i64) -> Span:
    return Span{start, finish}

def build(start: i64) -> i64:
    inferred: Span = Span(start:)
    explicit: Span = Span(start:, finish: start + 2)
    raw: Span = Span{start, finish: start + 3}
    return inferred.finish + explicit.finish + raw.finish
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
		t.Fatal("expected lowered __init__ call for one-arg paren constructor")
	}
	if len(inferredCall.LoweredArgs()) != 1 {
		t.Fatalf("expected one lowered arg for one-arg __init__, got %d", len(inferredCall.LoweredArgs()))
	}
	explicitDecl, ok := buildDecl.Body[1].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected explicit var decl, got %T", buildDecl.Body[1])
	}
	explicitLit, ok := explicitDecl.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected explicit constructor expr, got %T", explicitDecl.Value)
	}
	explicitCall := result.InitCalls[explicitLit]
	if explicitCall == nil {
		t.Fatal("expected lowered __init__ call for two-arg paren constructor")
	}
	if len(explicitCall.LoweredArgs()) != 2 {
		t.Fatalf("expected two lowered args for two-arg __init__, got %d", len(explicitCall.LoweredArgs()))
	}
	rawDecl, ok := buildDecl.Body[2].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected raw var decl, got %T", buildDecl.Body[2])
	}
	rawLit, ok := rawDecl.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected brace constructor expr, got %T", rawDecl.Value)
	}
	if result.InitCalls[rawLit] != nil {
		t.Fatal("expected brace struct literal to bypass __init__ lowering")
	}
}

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
