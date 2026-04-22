package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
)

func TestAnalyzeExtensionMethodCallRewritesToInternalFunctionCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extension_method_rewrite.llcontext", `
const enum Tok of i8:
    PLUS = 0

impl Tok:
    def score(self: Tok) -> i64:
        return 7

struct Box:
    value: i64

impl Box:
    def checksum(self: Box) -> i64:
        return self.value + 1

def read(tok: Tok, box: Box) -> i64:
    return tok.score() + box.checksum()
`)

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl, ok := funcSym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected read decl, got %T", funcSym.Node)
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	binary, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary return expr, got %T", ret.Value)
	}
	leftCall, ok := binary.Left.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected left rewritten call, got %T", binary.Left)
	}
	leftCallee, ok := leftCall.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected left callee ident after rewrite, got %T", leftCall.Func)
	}
	if !strings.HasPrefix(leftCallee.Name, "__ext__") {
		t.Fatalf("expected mangled extension method callee, got %q", leftCallee.Name)
	}
	if len(leftCall.Args) != 1 {
		t.Fatalf("expected receiver to be inserted as the first argument, got %d args", len(leftCall.Args))
	}
	if receiver, ok := leftCall.Args[0].(*ast.Ident); !ok || receiver.Name != "tok" {
		t.Fatalf("expected inserted receiver arg tok, got %T %#v", leftCall.Args[0], leftCall.Args[0])
	}

	rightCall, ok := binary.Right.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected right rewritten call, got %T", binary.Right)
	}
	rightCallee, ok := rightCall.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected right callee ident after rewrite, got %T", rightCall.Func)
	}
	if !strings.HasPrefix(rightCallee.Name, "__ext__") {
		t.Fatalf("expected mangled extension method callee, got %q", rightCallee.Name)
	}
	if len(rightCall.Args) != 1 {
		t.Fatalf("expected receiver to be inserted as the first argument, got %d args", len(rightCall.Args))
	}
	if receiver, ok := rightCall.Args[0].(*ast.Ident); !ok || receiver.Name != "box" {
		t.Fatalf("expected inserted receiver arg box, got %T %#v", rightCall.Args[0], rightCall.Args[0])
	}
}

func TestAnalyzeExtensionMethodPrefersRealFieldFunctionValues(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extension_method_field_precedence.llcontext", `
struct CallbackBox:
    run: func() -> i64

impl CallbackBox:
    def run(self: CallbackBox) -> i64:
        return 99

const ZERO: i64 = 0

def identity() -> i64:
    return 7

def read(box: CallbackBox) -> i64:
    return box.run()
`)

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl, ok := funcSym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected read decl, got %T", funcSym.Node)
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	if _, ok := call.Func.(*ast.FieldExpr); !ok {
		t.Fatalf("expected real function-valued field call to stay as field access, got %T", call.Func)
	}
	if len(call.Args) != 0 {
		t.Fatalf("expected no receiver rewriting for real field function call, got %d args", len(call.Args))
	}
}

func TestAnalyzeExtensionMethodNamedArgsAndDoBlock(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extension_method_named_args.llcontext", `
struct Box:
    value: i64

impl Box:
    def adjust(self: Box, delta: i64, scale: i64) -> i64:
        return self.value + delta * scale

def read(box: Box) -> i64:
    return box.adjust(scale: 3, delta: do:
        seed = 4
        seed
    )
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl, ok := funcSym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected read decl, got %T", funcSym.Node)
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected rewritten call, got %T", ret.Value)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || !strings.HasPrefix(callee.Name, "__ext__") {
		t.Fatalf("expected mangled extension callee, got %T %#v", call.Func, call.Func)
	}
	if len(call.LoweredArgs()) != 3 {
		t.Fatalf("expected receiver plus two ordered explicit args, got %d", len(call.LoweredArgs()))
	}
	if receiver, ok := call.LoweredArgs()[0].(*ast.Ident); !ok || receiver.Name != "box" {
		t.Fatalf("expected lowered receiver arg box, got %T %#v", call.LoweredArgs()[0], call.LoweredArgs()[0])
	}
	if _, ok := call.LoweredArgs()[1].(*ast.ExprBlock); !ok {
		t.Fatalf("expected reordered delta arg to be do block, got %T", call.LoweredArgs()[1])
	}
	if lit, ok := call.LoweredArgs()[2].(*ast.IntLit); !ok || lit.Value != "3" {
		t.Fatalf("expected reordered scale arg 3, got %T %#v", call.LoweredArgs()[2], call.LoweredArgs()[2])
	}
}

func TestAnalyzeUFCSFreeFunctionRewrite(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_free_function.llcontext", `
struct Box:
    value: i64

def scale(box: Box, delta: i64) -> i64:
    return box.value + delta

def read(box: Box) -> i64:
    return box.scale(5)
`)

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl := funcSym.Node.(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected rewritten call, got %T", ret.Value)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "scale" {
		t.Fatalf("expected UFCS callee scale, got %T %#v", call.Func, call.Func)
	}
	if len(call.LoweredArgs()) != 2 {
		t.Fatalf("expected receiver plus one arg, got %d", len(call.LoweredArgs()))
	}
	if receiver, ok := call.LoweredArgs()[0].(*ast.Ident); !ok || receiver.Name != "box" {
		t.Fatalf("expected inserted receiver arg box, got %T %#v", call.LoweredArgs()[0], call.LoweredArgs()[0])
	}
}

func TestAnalyzeUFCSFreeFunctionAutorefRewrite(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_free_function_autoref.llcontext", `
struct Box:
    value: i64

def score_ref(box: Box&, delta: i64 = 1) -> i64:
    return box.value + delta

def read(box: Box) -> i64:
    return box.score_ref(5)
`)

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl := funcSym.Node.(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected rewritten call, got %T", ret.Value)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "score_ref" {
		t.Fatalf("expected UFCS callee score_ref, got %T %#v", call.Func, call.Func)
	}
	if len(call.LoweredArgs()) != 2 {
		t.Fatalf("expected receiver plus one lowered arg, got %d", len(call.LoweredArgs()))
	}
	addr, ok := call.LoweredArgs()[0].(*ast.AddrOfExpr)
	if !ok {
		t.Fatalf("expected UFCS receiver autoref, got %T", call.LoweredArgs()[0])
	}
	if ident, ok := addr.Operand.(*ast.Ident); !ok || ident.Name != "box" {
		t.Fatalf("expected UFCS autoref operand box, got %T %#v", addr.Operand, addr.Operand)
	}
	if lit, ok := call.LoweredArgs()[1].(*ast.IntLit); !ok || lit.Value != "5" {
		t.Fatalf("expected UFCS delta arg 5, got %T %#v", call.LoweredArgs()[1], call.LoweredArgs()[1])
	}
}

func TestAnalyzeUFCSAmbiguityReportsCandidates(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ufcs_ambiguous.llcontext", `
namespace left:
    struct Box:
        value: i64

    def score(box: Box) -> i64:
        return box.value

namespace right:
    def score(box: left.Box) -> i64:
        return box.value + 1

using left
using right

def read(box: Box) -> i64:
    return box.score()
`)
	if len(result.Errors()) == 0 {
		t.Fatal("expected UFCS ambiguity error")
	}
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `UFCS call "score"`) || !strings.Contains(all, "left.score") || !strings.Contains(all, "right.score") {
		t.Fatalf("expected UFCS ambiguity diagnostic with candidates, got:\n%s", all)
	}
}

func TestAnalyzeOptionalChainingOnOptionalAndNullableReceivers(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "optional_chaining.llcontext", `
struct Box:
    value: i64

def score(box: Box, delta: i64 = 1) -> i64:
    return box.value + delta

def score_ref(box: Box&, delta: i64 = 1) -> i64:
    return box.value + delta

def read(maybe_box: Box?, maybe_ref: Box&?) -> void:
    _ = maybe_box?.value
    _ = maybe_box?.score()
    _ = maybe_ref?.score_ref(2)
`)
	fn := result.File.Decls[3].(*ast.FuncDecl)
	first := fn.Body[0].(*ast.DiscardStmt).Value
	second := fn.Body[1].(*ast.DiscardStmt).Value
	third := fn.Body[2].(*ast.DiscardStmt).Value
	if got := result.ExprTypes[first].String(); got != "i64?" {
		t.Fatalf("expected optional safe-field type i64?, got %s", got)
	}
	if got := result.ExprTypes[second].String(); got != "i64?" {
		t.Fatalf("expected optional safe-call type i64?, got %s", got)
	}
	if got := result.ExprTypes[third].String(); got != "i64?" {
		t.Fatalf("expected nullable-ref safe-call type i64?, got %s", got)
	}
}
