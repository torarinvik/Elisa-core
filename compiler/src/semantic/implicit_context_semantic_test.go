package semantic

import (
	"testing"

	"llcontext/src/ast"
)

func TestAnalyzeGenericCallResolvesImplicitContextArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_context_generic.llcontext", `context ParseCtx:
    parser: i64
    alloc: i64

def inner[T]() with ParseCtx -> i64:
    return parser + alloc

def outer[T]() with ParseCtx -> i64:
    return inner[T]() with ParseCtx()
`)

	var outer *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "outer" {
			outer = fn
			break
		}
	}
	if outer == nil {
		t.Fatal("expected outer function declaration")
	}
	if len(outer.Body) != 1 {
		t.Fatalf("expected single-statement outer body, got %d", len(outer.Body))
	}
	ret, ok := outer.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		t.Fatalf("expected return statement with one value, got %#v", outer.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected return value to be a call expression, got %T", ret.Value)
	}
	if !call.ResolvedImplicitArgsValid {
		t.Fatalf("expected resolved implicit args to be marked valid: %#v", call)
	}
	if len(call.ResolvedImplicitArgs) != 2 {
		t.Fatalf("expected 2 resolved implicit args, got %d", len(call.ResolvedImplicitArgs))
	}
	for i, arg := range call.ResolvedImplicitArgs {
		ident, ok := arg.(*ast.Ident)
		if !ok {
			t.Fatalf("expected implicit arg %d to be identifier, got %T", i, arg)
		}
		if ident.Name != []string{"parser", "alloc"}[i] {
			t.Fatalf("expected implicit arg %d to be %q, got %q", i, []string{"parser", "alloc"}[i], ident.Name)
		}
	}
}

func TestAnalyzeGenericCallAutoForwardsFunctionImplicitContextArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_context_forward_function.llcontext", `context ParseCtx:
    parser: i64
    alloc: i64

def inner[T]() with ParseCtx -> i64:
    return parser + alloc

def outer[T]() with ParseCtx -> i64:
    return inner[T]()
`)

	var outer *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "outer" {
			outer = fn
			break
		}
	}
	if outer == nil {
		t.Fatal("expected outer function declaration")
	}
	ret, ok := outer.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		t.Fatalf("expected return statement with one value, got %#v", outer.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected return value to be a call expression, got %T", ret.Value)
	}
	if !call.ResolvedImplicitArgsValid {
		t.Fatalf("expected resolved implicit args to be marked valid: %#v", call)
	}
	if len(call.ResolvedImplicitArgs) != 2 {
		t.Fatalf("expected 2 resolved implicit args, got %d", len(call.ResolvedImplicitArgs))
	}
	for i, arg := range call.ResolvedImplicitArgs {
		ident, ok := arg.(*ast.Ident)
		if !ok {
			t.Fatalf("expected implicit arg %d to be identifier, got %T", i, arg)
		}
		if ident.Name != []string{"parser", "alloc"}[i] {
			t.Fatalf("expected implicit arg %d to be %q, got %q", i, []string{"parser", "alloc"}[i], ident.Name)
		}
	}
}

func TestAnalyzeTryWrappedGenericCallResolvesImplicitContextArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_context_try_generic.llcontext", `error ParseErr:
    Bad

context ParseCtx:
    parser: i64
    alloc: i64

def inner[T]() with ParseCtx -> i64 error[ParseErr]:
    return 1

def outer[T]() with ParseCtx -> i64 error[ParseErr]:
    return try inner[T]() with ParseCtx()
`)

	var outer *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "outer" {
			outer = fn
			break
		}
	}
	if outer == nil {
		t.Fatal("expected outer function declaration")
	}
	ret, ok := outer.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		t.Fatalf("expected return statement with one value, got %#v", outer.Body[0])
	}
	tryExpr, ok := ret.Value.(*ast.TryExpr)
	if !ok {
		t.Fatalf("expected return value to be try expression, got %T", ret.Value)
	}
	call, ok := tryExpr.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected try value to be a call expression, got %T", tryExpr.Value)
	}
	if !call.ResolvedImplicitArgsValid {
		t.Fatalf("expected resolved implicit args to be marked valid: %#v", call)
	}
	if len(call.ResolvedImplicitArgs) != 2 {
		t.Fatalf("expected 2 resolved implicit args, got %d", len(call.ResolvedImplicitArgs))
	}
}

func TestAnalyzeTryWrappedGenericCallAutoForwardsFunctionImplicitContextArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_context_try_forward_function.llcontext", `error ParseErr:
    Bad

context ParseCtx:
    parser: i64
    alloc: i64

def inner[T]() with ParseCtx -> i64 error[ParseErr]:
    return 1

def outer[T]() with ParseCtx -> i64 error[ParseErr]:
    return try inner[T]()
`)

	var outer *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "outer" {
			outer = fn
			break
		}
	}
	if outer == nil {
		t.Fatal("expected outer function declaration")
	}
	ret, ok := outer.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		t.Fatalf("expected return statement with one value, got %#v", outer.Body[0])
	}
	tryExpr, ok := ret.Value.(*ast.TryExpr)
	if !ok {
		t.Fatalf("expected return value to be try expression, got %T", ret.Value)
	}
	call, ok := tryExpr.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected try value to be a call expression, got %T", tryExpr.Value)
	}
	if !call.ResolvedImplicitArgsValid {
		t.Fatalf("expected resolved implicit args to be marked valid: %#v", call)
	}
	if len(call.ResolvedImplicitArgs) != 2 {
		t.Fatalf("expected 2 resolved implicit args, got %d", len(call.ResolvedImplicitArgs))
	}
}
