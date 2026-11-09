package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestAnalyzeUFCSPrefersGenericStructFieldOverSameNamedUFCSFunction(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_generic_field_precedence.elisa", `
struct Cell[T]:
    value: T

struct IndexMap[K, T]:
    marker: u8

def value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    _ = map
    _ = index
    return fallback

def read(cell: Cell[i64]) -> i64:
    return cell.value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl := funcSym.Node.(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	field, ok := ret.Value.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field expr return, got %T", ret.Value)
	}
	if field.Field != "value" {
		t.Fatalf("expected value field access, got %#v", field)
	}
	if got := result.ExprTypes[field]; got == nil || got.String() != "i64" {
		t.Fatalf("expected field type i64, got %v", got)
	}
}

func TestAnalyzeUFCSPrefersPlainCountFieldOverSameNamedUFCSFunction(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_count_field_precedence.elisa", `
struct Counter:
    count: usize

struct IndexMap[K, T]:
    marker: u8

def count[K, T](map: IndexMap[K, T]&) -> usize:
    _ = map
    return 0

def read(counter: Counter) -> usize:
    return counter.count
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl := funcSym.Node.(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	field, ok := ret.Value.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field expr return, got %T", ret.Value)
	}
	if field.Field != "count" {
		t.Fatalf("expected count field access, got %#v", field)
	}
	if got := result.ExprTypes[field]; got == nil || got.String() != "usize" {
		t.Fatalf("expected field type usize, got %v", got)
	}
}

func TestAnalyzeUFCSDoesNotHijackGenericEntryValueField(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_generic_entry_value.elisa", `
struct Entry[T]:
    value: T

struct IndexMap[K, T]:
    marker: u8

def entry[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> Entry[T]:
    _ = map
    _ = index
    return Entry[T]{value: fallback}

def value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    _ = map
    _ = index
    return fallback

def read_value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    return entry[K, T](map, index, fallback).value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}

	funcSym, ok := result.GlobalScope.Lookup("read_value")
	if !ok {
		t.Fatal("expected read_value symbol")
	}
	decl := funcSym.Node.(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	field, ok := ret.Value.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field expr return, got %T", ret.Value)
	}
	if field.Field != "value" {
		t.Fatalf("expected value field access, got %#v", field)
	}
}

func TestAnalyzeUFCSDoesNotHijackGenericEntryValueInStructLiteral(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_generic_entry_value_structlit.elisa", `
struct Entry[T]:
    value: T

struct Wrap[T]:
    value: T

struct IndexMap[K, T]:
    marker: u8

def value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    _ = map
    _ = index
    return fallback

def wrap_entry[T](entry: Entry[T]) -> Wrap[T]:
    return Wrap[T]{value: entry.value}
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeUFCSGlobalNameDoesNotShadowLocalValueBinding(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_local_value_shadow.elisa", `
struct IndexMap[K, T]:
    marker: u8

def value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    _ = map
    _ = index
    return fallback

def read() -> i64:
    value: i64 = 7
    return value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeUFCSGlobalNameDoesNotShadowInferredValueBinding(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_local_inferred_value_shadow.elisa", `
struct IndexMap[K, T]:
    marker: u8

def value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    _ = map
    _ = index
    return fallback

def read() -> i64:
    value = 7
    return value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeUFCSGlobalNameDoesNotShadowTupleReturnLocalValue(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_tuple_return_local_value.elisa", `
struct IndexMap[K, T]:
    marker: u8

def value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    _ = map
    _ = index
    return fallback

def read(flag: bool) -> (value: bool, after: usize):
    value: mutable bool = flag
    after: mutable usize = 7
    return (value, after)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeMethodFunctionSupportsReceiverCallWithoutGlobalCollision(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "method_value_receiver.elisa", `
struct IndexMap[K, T]:
    marker: u8


def value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    _ = map
    _ = index
    return fallback

def read[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    return map.value(index, fallback)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeMethodFunctionDoesNotShadowTupleValueBindings(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "method_tuple_value_binding.elisa", `
struct IndexMap[K, T]:
    marker: u8


def value[K, T](map: IndexMap[K, T]&, index: usize, fallback: T) -> T:
    _ = map
    _ = index
    return fallback

def read(flag: bool) -> (value: bool, after: usize):
    value: mutable bool = flag
    after: mutable usize = 7
    return (value, after)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeMethodFunctionSupportsGenericReceiverCalls(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "method_generic_receivers.elisa", `
extern RegistrySlot
type RegistryId = id[RegistrySlot]

struct SymbolEntry[T]:
    value: T

struct Registry[K, T]:
    marker: u8


def value[K, T](table: Registry[K, T]&, item_id: RegistryId, fallback: T) -> T:
    _ = table
    _ = item_id
    return fallback


def entry[K, T](table: Registry[K, T]&, item_id: RegistryId, fallback: T) -> SymbolEntry[T]:
    _ = table
    _ = item_id
    return SymbolEntry[T]{value: fallback}

def read[T](table: Registry[cstr, T]&, item_id: RegistryId, fallback: T) -> T:
    _ = table.entry(item_id, fallback)
    return table.value(item_id, fallback)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeExternMethodFunctionSupportsReceiverCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extern_method_receiver.elisa", `
struct ReadBox[T]:
    marker: u8


extern finish[T](box: ReadBox[T]&) -> darray[T]

def read(box: ReadBox[i64]&) -> darray[i64]:
    return box.finish()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeUFCSAmbiguityReportsCandidates(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ufcs_ambiguous.elisa", `
module left:
    struct Box:
        value: i64

    def score(box: Box) -> i64:
        return box.value

module right:
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

// `recv.PascalCase()` (zero-arg postfix shorthand) is parsed as a cast to type
// `PascalCase`; when that name is not a type it falls back to the call
// `PascalCase(recv)`, so PascalCase names resolve to UFCS methods just like the
// lowercase and multi-arg forms.
func TestAnalyzePostfixShorthandFallsBackToPascalCaseUFCSMethod(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "postfix_pascalcase_ufcs.elisa", `
struct Handle:
    n: mutable i64


def IsOpen(self: Handle&) -> bool:
    return self.n != 0


def Reset(self: mutable Handle&) -> void:
    self.n <- 0

def read(h: mutable Handle&) -> bool:
    h.Reset()
    return h.IsOpen()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

// The postfix-shorthand cast to a real type (e.g. `x.u64()`) must keep casting,
// not be re-interpreted as a call — the fallback only applies when the target is
// not a type.
func TestAnalyzePostfixShorthandStillCastsToRealType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "postfix_cast_real_type.elisa", `
def widen(x: i64) -> u64:
    return x.u64()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

// Uniform call syntax: a function overloaded by first-parameter type is callable
// in all four forms (dot/free × each receiver), including a string literal as a
// `cstr` receiver — `"x".open()` resolves the same as the free call `open("x")`.
func TestAnalyzeUniformCallSyntaxOverloadIncludingStringLiteralReceiver(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "uniform_call_overload.elisa", `
struct Door:
    is_open: mutable bool

struct File:
    h: mutable i64

def open(path: cstr) -> File?:
    _ = path
    return null

def open(door: mutable Door&) -> void:
    door.is_open <- true

def drive(d: mutable Door&) -> void:
    d.open()
    open(d)
    _ = open("file.txt")
    _ = "file.txt".open()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeUFCSRefOverloadRespectsMutability(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_ref_overload_mutability.elisa", `
struct Box:
    value: mutable i64

def lookup(box: Box&) -> i64&?:
    return null

def lookup(box: mutable Box&) -> mutable i64&?:
    return null

def read_mut(box: mutable Box&) -> mutable i64&?:
    return box.lookup()

def read_imm(box: Box&) -> i64&?:
    return box.lookup()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	mutSym, ok := result.GlobalScope.Lookup("read_mut")
	if !ok {
		t.Fatal("expected read_mut symbol")
	}
	mutCall := mutSym.Node.(*ast.FuncDecl).Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if got := result.ExprTypes[mutCall]; got == nil || got.String() != "mutable i64&?" {
		t.Fatalf("expected mutable receiver lookup to return mutable i64&?, got %T %v", got, got)
	}
	immSym, ok := result.GlobalScope.Lookup("read_imm")
	if !ok {
		t.Fatal("expected read_imm symbol")
	}
	immCall := immSym.Node.(*ast.FuncDecl).Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if got := result.ExprTypes[immCall]; got == nil || got.String() != "i64&?" {
		t.Fatalf("expected immutable receiver lookup to return i64&?, got %T %v", got, got)
	}
}

// Primitive values are valid UFCS receivers — including a bare integer literal,
// whose untyped `int` type is accepted against an integer-typed first parameter.
func TestAnalyzePrimitiveAndLiteralReceiversResolveUFCS(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "primitive_receiver.elisa", `
def inc(n: i64) -> i64:
    return n + 1

def via_value(x: i64) -> i64:
    return x.inc()

def via_literal() -> i64:
    return 7.inc()
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestUFCSUntypedIntegerReceiverMatchesOnlyIntegralTypes(t *testing.T) {
	a := &Analyzer{}
	actual := Type(&BuiltinType{Name: "int"})
	for _, name := range []string{"char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr"} {
		if !a.ufcsReceiverAssignableTo(&BuiltinType{Name: name}, actual) {
			t.Errorf("untyped integer receiver should match integral type %q", name)
		}
	}
	for _, name := range []string{"bool", "string", "cstr", "sview", "void", "f32", "f64"} {
		if a.ufcsReceiverAssignableTo(&BuiltinType{Name: name}, actual) {
			t.Errorf("untyped integer receiver must not match non-integral type %q", name)
		}
	}
}
