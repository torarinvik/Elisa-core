package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeDefaultArgsOnDirectCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "default_args_direct.elisa", `def add(x: i64, y: i64 = 7) -> i64:
    return x + y

def build() -> i64:
    return add(5)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	buildDecl, ok := buildSym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected build decl, got %T", buildSym.Node)
	}
	ret := buildDecl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 2 {
		t.Fatalf("expected 2 resolved args, got %#v", call.ResolvedArgs)
	}
	if lit, ok := call.ResolvedArgs[1].(*ast.IntLit); !ok || lit.Value != "7" {
		t.Fatalf("expected cloned default arg 7, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
}

func TestAnalyzeDefaultArgsWithContextualLiterals(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "default_args_contextual_literals.elisa", `def consume(values: darray[i64] = [], cond: i64? = null) -> i64:
    if cond == null:
        return values.count
    return 1

def build() -> i64:
    return consume()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 2 {
		t.Fatalf("expected 2 resolved args, got %#v", call.ResolvedArgs)
	}
	if _, ok := call.ResolvedArgs[0].(*ast.ListLitExpr); !ok {
		t.Fatalf("expected contextual list default, got %T", call.ResolvedArgs[0])
	}
	if _, ok := call.ResolvedArgs[1].(*ast.NullLit); !ok {
		t.Fatalf("expected contextual null default, got %T", call.ResolvedArgs[1])
	}
}

func TestAnalyzeDefaultArgsThroughLocalAlias(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "default_args_local_alias.elisa", `def add(x: i64, y: i64 = 7) -> i64:
    return x + y

def build() -> i64:
    f = add
    return f(5)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 2 {
		t.Fatalf("expected 2 resolved args through alias, got %#v", call.ResolvedArgs)
	}
	if lit, ok := call.ResolvedArgs[1].(*ast.IntLit); !ok || lit.Value != "7" {
		t.Fatalf("expected default arg 7 through alias, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
}

func TestAnalyzeCallArgForwardingFillsSameNameParams(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "call_arg_forwarding_basic.elisa", `def consume(parser: i64, offset: i64, width: i64 = 9) -> i64:
    return parser + offset + width

def build(parser: i64, offset: i64) -> i64:
    return consume(..)
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 3 {
		t.Fatalf("expected 3 resolved args, got %#v", call.ResolvedArgs)
	}
	if ident, ok := call.ResolvedArgs[0].(*ast.Ident); !ok || ident.Name != "parser" {
		t.Fatalf("expected forwarded parser arg, got %T %#v", call.ResolvedArgs[0], call.ResolvedArgs[0])
	}
	if ident, ok := call.ResolvedArgs[1].(*ast.Ident); !ok || ident.Name != "offset" {
		t.Fatalf("expected forwarded offset arg, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
	if lit, ok := call.ResolvedArgs[2].(*ast.IntLit); !ok || lit.Value != "9" {
		t.Fatalf("expected trailing default arg 9, got %T %#v", call.ResolvedArgs[2], call.ResolvedArgs[2])
	}
}

func TestAnalyzeCallArgForwardingExplicitArgsOverrideForwardedOnes(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "call_arg_forwarding_override.elisa", `def consume(parser: i64, offset: i64, width: i64 = 9) -> i64:
    return parser + offset + width

def build(parser: i64, offset: i64) -> i64:
    return consume(.., offset: 5)
`)
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if lit, ok := call.ResolvedArgs[1].(*ast.IntLit); !ok || lit.Value != "5" {
		t.Fatalf("expected explicit offset arg to override forwarded one, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
}

func TestAnalyzeCallArgForwardingIgnoresNonValueSymbols(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "call_arg_forwarding_non_value_ignored.elisa", `struct value:
    inner: i64

def consume(value: i64) -> i64:
    return value

def build() -> i64:
    return consume(..)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `function "consume" is missing argument for parameter "value"`) {
		t.Fatalf("expected missing argument diagnostic after ignoring non-value symbol, got:\n%s", all)
	}
}

func TestAnalyzeCallArgForwardingRejectsVariadicCalls(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "call_arg_forwarding_variadic_rejected.elisa", `extern consume(fmt: i64, ...) -> void

def build(fmt: i64) -> void:
    consume(..)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "call argument forwarding `..` is not supported for variadic function") {
		t.Fatalf("expected variadic forwarding diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeNamedDefaultArgsFillTrailingSubset(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "default_args_named_subset.elisa", `def sum3(x: i64, y: i64 = 1, z: i64 = 2) -> i64:
    return x + y + z

def build() -> i64:
    return sum3(z: 9, x: 5)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	buildSym, _ := result.GlobalScope.Lookup("build")
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 3 {
		t.Fatalf("expected 3 resolved args, got %#v", call.ResolvedArgs)
	}
	if lit, ok := call.ResolvedArgs[1].(*ast.IntLit); !ok || lit.Value != "1" {
		t.Fatalf("expected middle default arg 1, got %T %#v", call.ResolvedArgs[1], call.ResolvedArgs[1])
	}
	if lit, ok := call.ResolvedArgs[2].(*ast.IntLit); !ok || lit.Value != "9" {
		t.Fatalf("expected named z arg 9, got %T %#v", call.ResolvedArgs[2], call.ResolvedArgs[2])
	}
}

func TestAnalyzeExtensionMethodDefaultArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "default_args_extension_method.elisa", `struct Box:
    value: i64

impl Box:
    def adjust(self: Box, delta: i64 = 7) -> i64:
        return self.value + delta

def read(box: Box) -> i64:
    return box.adjust()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	readSym, _ := result.GlobalScope.Lookup("read")
	readDecl := readSym.Node.(*ast.FuncDecl)
	ret := readDecl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	lowered := call.LoweredArgs()
	if len(lowered) != 2 {
		t.Fatalf("expected receiver plus default arg, got %d args", len(lowered))
	}
	if recv, ok := lowered[0].(*ast.Ident); !ok || recv.Name != "box" {
		t.Fatalf("expected receiver arg box, got %T %#v", lowered[0], lowered[0])
	}
	if lit, ok := lowered[1].(*ast.IntLit); !ok || lit.Value != "7" {
		t.Fatalf("expected default extension arg 7, got %T %#v", lowered[1], lowered[1])
	}
}

func TestAnalyzeRejectsNonTrailingDefaultParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "default_args_non_trailing_rejected.elisa", `def bad(x: i64 = 1, y: i64) -> i64:
    return x + y
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "must declare a default because it follows a defaulted parameter") {
		t.Fatalf("expected non-trailing default diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsParamReferencedInDefault(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "default_args_param_reference_rejected.elisa", `def bad(x: i64, y: i64 = x) -> i64:
    return x + y
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, UndefinedIdentifierMessage("x")) {
		t.Fatalf("expected undefined identifier diagnostic for param-referencing default, got:\n%s", all)
	}
}

func TestFunctionTypeComparisonIgnoresDefaultArgumentMetadata(t *testing.T) {
	intType := &BuiltinType{Name: "i64"}
	left := &FuncType{
		Name:                      "f",
		Params:                    []Type{intType},
		ExplicitParamCount:        1,
		Return:                    intType,
		ExplicitParamDefaultExprs: []ast.Expr{&ast.IntLit{Value: "1"}},
		ExplicitParamHasDefault:   []bool{true},
	}
	right := &FuncType{
		Name:               "f",
		Params:             []Type{intType},
		ExplicitParamCount: 1,
		Return:             intType,
	}
	if !SameType(left, right) || !SameType(right, left) {
		t.Fatalf("expected SameType to ignore default metadata")
	}
	if !AssignableTo(left, right) || !AssignableTo(right, left) {
		t.Fatalf("expected AssignableTo to ignore default metadata")
	}
}
