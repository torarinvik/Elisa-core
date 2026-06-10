package semantic

import (
	"testing"

	"elisacore/src/ast"
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

func TestAnalyzeTreeStoreImplicitParamFollowsEnumPayloadStructs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_tree_store_enum_payload.elisa", `tree Syntax:
	node Seq:
		Empty

struct RuntimeSyntax:
	raw: Syntax.Seq

enum RuntimeValue:
	Nil
	Syntax(RuntimeSyntax)

def identity(value: RuntimeValue) -> RuntimeValue:
	return value
`)

	sym, ok := result.GlobalScope.Lookup("identity")
	if !ok || sym == nil {
		t.Fatal("expected identity symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected identity to have function type, got %T", sym.Type)
	}
	if len(fnType.ImplicitParamNames) != 1 {
		t.Fatalf("expected one implicit tree store param, got %v", fnType.ImplicitParamNames)
	}
	if got := fnType.ImplicitParamNames[0]; got != "__tree_store_Syntax" {
		t.Fatalf("expected Syntax implicit tree store param, got %q", got)
	}
	if _, ok := fnType.Params[len(fnType.Params)-1].(*TreeStoreType); !ok {
		t.Fatalf("expected final param to be tree store, got %T", fnType.Params[len(fnType.Params)-1])
	}
}

func TestAnalyzeTreeStoreImplicitParamSkipsExplicitStoreParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_tree_store_explicit_param.elisa", `tree Syntax:
	node Expr:
		Int(value: i64)

def read(store: Syntax.Store[Local], expr: Syntax.Expr) -> i64:
	in store:
		if expr is Syntax.Expr.Int:
			return expr.value
		return 0
`)

	sym, ok := result.GlobalScope.Lookup("read")
	if !ok || sym == nil {
		t.Fatal("expected read symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read to have function type, got %T", sym.Type)
	}
	if len(fnType.ImplicitParamNames) != 0 {
		t.Fatalf("expected no duplicate implicit tree store param, got %v", fnType.ImplicitParamNames)
	}
}

func TestAnalyzeTreeStoreImplicitParamRecordsArenaScopeReads(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_tree_store_arena_scope.elisa", `tree Syntax:
	node Expr:
		Int(value: i64)

def read_with_arena(owner: Arena, expr: Syntax.Expr) -> i64:
	can Memory.Allocate:
		in owner:
			if expr is Syntax.Expr.Int:
				return expr.value
		return 0
`)

	sym, ok := result.GlobalScope.Lookup("read_with_arena")
	if !ok || sym == nil {
		t.Fatal("expected read_with_arena symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_with_arena to have function type, got %T", sym.Type)
	}
	if len(fnType.ImplicitParamNames) != 1 {
		t.Fatalf("expected arena-scope incoming tree read to carry hidden store, got %v", fnType.ImplicitParamNames)
	}
	if got := fnType.ImplicitParamNames[0]; got != "__tree_store_Syntax" {
		t.Fatalf("expected Syntax implicit tree store param, got %q", got)
	}
}

func TestAnalyzeTreeStoreImplicitParamFollowsRefContainers(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_tree_store_ref_container.elisa", `tree Pattern:
	node Item:
		Capture(name: sview)

struct Rule:
	pattern: darray[Pattern.Item]

def copy_rules(owner: Arena, rules: mutable darray[Rule]&):
	pass
`)

	sym, ok := result.GlobalScope.Lookup("copy_rules")
	if !ok || sym == nil {
		t.Fatal("expected copy_rules symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected copy_rules to have function type, got %T", sym.Type)
	}
	if len(fnType.ImplicitParamNames) != 1 {
		t.Fatalf("expected ref container with tree handles to carry hidden store, got %v", fnType.ImplicitParamNames)
	}
	if got := fnType.ImplicitParamNames[0]; got != "__tree_store_Pattern" {
		t.Fatalf("expected Pattern implicit tree store param, got %q", got)
	}
}

func TestAnalyzeCastHookDoesNotInferTreeStoreFromInactiveReturnVariants(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_tree_store_cast_hook_return_variant.elisa", `tree Syntax:
	node Expr:
		Int(value: i64)

struct Closure:
	body: Syntax.Expr

enum Value:
	Bool(value: bool)
	Function(closure: Closure)

def __cast__(value: bool) -> Value:
	return Value.Bool(value)

def use_cast() -> Value:
	return true.Value()
`)

	var sym *Symbol
	for _, candidate := range result.CastHooks {
		if candidate != nil {
			sym = candidate
			break
		}
	}
	if sym == nil {
		t.Fatalf("expected __cast__ symbol; cast hooks=%d diagnostics=%v", len(result.CastHooks), result.Diagnostics)
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected __cast__ to have function type, got %T", sym.Type)
	}
	if len(fnType.ImplicitParamNames) != 0 {
		t.Fatalf("expected cast hook to avoid return-only inactive tree store inference, got %v", fnType.ImplicitParamNames)
	}
}
