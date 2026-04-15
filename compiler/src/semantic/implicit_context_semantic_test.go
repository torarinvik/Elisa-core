package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
)

func findImplicitContextTestFuncDecl(t *testing.T, result *Result, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("expected %s function declaration", name)
	return nil
}

func TestAnalyzeGenericCallResolvesImplicitContextArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_context_generic.llcontext", `context ParseCtx:
    parser: i64
    alloc: i64

def inner[T]() with ParseCtx -> i64:
    return parser + alloc

def outer[T]() with ParseCtx -> i64:
    return inner[T]() with ParseCtx(..)
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
    return try inner[T]() with ParseCtx(..)
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

func TestAnalyzeWithStmtBundleSpreadUsesAmbientBindings(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_context_with_stmt_spread.llcontext", `context ParseCtx:
    parser: i64
    alloc: i64

def inner() with ParseCtx -> i64:
    return parser + alloc

def keep() -> i64:
    parser: i64 = 7
    alloc: i64 = 9
    with ParseCtx(..):
        return inner()
`)

	keep := findImplicitContextTestFuncDecl(t, result, "keep")
	withStmt, ok := keep.Body[2].(*ast.WithStmt)
	if !ok {
		t.Fatalf("expected with stmt, got %T", keep.Body[2])
	}
	ret, ok := withStmt.Body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		t.Fatalf("expected return stmt in with body, got %#v", withStmt.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected return value to be call expr, got %T", ret.Value)
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

func TestAnalyzeWithStmtBundleSpreadExplicitOverrideWins(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_context_with_stmt_override.llcontext", `context ParseCtx:
    parser: i64
    alloc: i64

def inner() with ParseCtx -> i64:
    return parser + alloc

def keep() -> i64:
    parser: i64 = 7
    alloc: i64 = 9
    override_alloc: i64 = 11
    with ParseCtx(.., alloc = override_alloc):
        return inner()
`)

	keep := findImplicitContextTestFuncDecl(t, result, "keep")
	withStmt, ok := keep.Body[3].(*ast.WithStmt)
	if !ok {
		t.Fatalf("expected with stmt, got %T", keep.Body[3])
	}
	tempDecl, ok := withStmt.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected synthesized temp decl, got %T", withStmt.Body[0])
	}
	overrideRef, ok := tempDecl.Value.(*ast.Ident)
	if !ok || overrideRef.Name != "override_alloc" {
		t.Fatalf("expected temp decl to capture override_alloc, got %#v", tempDecl.Value)
	}
	ret, ok := withStmt.Body[1].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		t.Fatalf("expected return stmt after temp decl, got %#v", withStmt.Body[1])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected return value to be call expr, got %T", ret.Value)
	}
	if len(call.ResolvedImplicitArgs) != 2 {
		t.Fatalf("expected 2 resolved implicit args, got %d", len(call.ResolvedImplicitArgs))
	}
	overrideArg, ok := call.ResolvedImplicitArgs[1].(*ast.Ident)
	if !ok {
		t.Fatalf("expected overridden alloc arg to be identifier, got %T", call.ResolvedImplicitArgs[1])
	}
	if overrideArg.Name != tempDecl.Name {
		t.Fatalf("expected overridden alloc arg to use synthesized temp %q, got %q", tempDecl.Name, overrideArg.Name)
	}
}

func TestAnalyzeTrailingWithBundleSpreadExplicitOverrideWins(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_context_call_override.llcontext", `context ParseCtx:
    parser: i64
    alloc: i64

def inner() with ParseCtx -> i64:
    return parser + alloc

def keep() -> i64:
    parser: i64 = 7
    alloc: i64 = 9
    override_alloc: i64 = 11
    return inner() with ParseCtx(.., alloc = override_alloc)
`)

	keep := findImplicitContextTestFuncDecl(t, result, "keep")
	ret, ok := keep.Body[3].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		t.Fatalf("expected return stmt, got %#v", keep.Body[3])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected return value to be call expr, got %T", ret.Value)
	}
	if len(call.ResolvedImplicitArgs) != 2 {
		t.Fatalf("expected 2 resolved implicit args, got %d", len(call.ResolvedImplicitArgs))
	}
	parserArg, ok := call.ResolvedImplicitArgs[0].(*ast.Ident)
	if !ok || parserArg.Name != "parser" {
		t.Fatalf("expected parser implicit arg, got %#v", call.ResolvedImplicitArgs[0])
	}
	overrideArg, ok := call.ResolvedImplicitArgs[1].(*ast.Ident)
	if !ok || overrideArg.Name != "override_alloc" {
		t.Fatalf("expected alloc implicit arg to use explicit override, got %#v", call.ResolvedImplicitArgs[1])
	}
}

func TestAnalyzeWithBundleWithoutSpreadRequiresAllFieldsExplicit(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "implicit_context_bundle_no_spread.llcontext", `context ParseCtx:
    parser: i64
    alloc: i64

def inner() with ParseCtx -> i64:
    return parser + alloc

def keep() -> i64:
    parser: i64 = 7
    alloc: i64 = 9
    return inner() with ParseCtx(parser = parser)
`)

	errs := result.Errors()
	if len(errs) == 0 {
		t.Fatal("expected semantic error for bundle field missing without spread")
	}
	if !strings.Contains(errs[0], "missing explicit value for \"alloc\" in context bundle \"ParseCtx\"") {
		t.Fatalf("expected missing explicit bundle field error, got: %v", errs)
	}
}

func TestAnalyzeWithBundleSpreadMissingAmbientFieldErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "implicit_context_bundle_missing_ambient.llcontext", `context ParseCtx:
    parser: i64
    alloc: i64

def inner() with ParseCtx -> i64:
    return parser + alloc

def keep() -> i64:
    parser: i64 = 7
    return inner() with ParseCtx(..)
`)

	errs := result.Errors()
	if len(errs) == 0 {
		t.Fatal("expected semantic error for missing ambient spread binding")
	}
	if !strings.Contains(errs[0], "missing same-name ambient value for \"alloc\" in context bundle \"ParseCtx\"") {
		t.Fatalf("expected missing ambient bundle field error, got: %v", errs)
	}
}
