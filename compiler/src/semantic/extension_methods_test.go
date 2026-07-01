package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeExtensionMethodCallRewritesToInternalFunctionCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "extension_method_rewrite.elisa", `
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
	result := analyzeFunctionAnalysisTestSource(t, "extension_method_field_precedence.elisa", `
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
	result := analyzeFunctionAnalysisTestSource(t, "extension_method_named_args.elisa", `
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
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_free_function.elisa", `
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

// A PascalCase UFCS method called on a struct FIELD and bound into a typed local
// (`size: u64 = self.f.GetSize()`) must resolve as the call `GetSize(self.f)`, NOT be
// misread as a cast to a type named GetSize. Regression: the widening-cast range-fact
// pass (recordWideningCastRangeFact) peeled the postfix-shorthand CastExpr and resolved
// its target ("GetSize") as a type un-suppressed, emitting a spurious `unknown type
// "GetSize"`. Only triggered in a var-decl whose RHS is a *bare* postfix call (a
// parenthesized RHS, a `return`, or a `_ =` discard dodged the pass).
func TestAnalyzeUFCSPascalCaseMethodOnFieldInTypedVarDecl(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ufcs_pascalcase_field_vardecl.elisa", `
struct Thing:
    n: i64

def GetSize(self: Thing&) -> u64:
    return 0

struct Box:
    f: mutable Thing

def use(self: mutable Box&) -> u64:
    size: u64 = self.f.GetSize()
    inferred = self.f.GetSize()
    return size + inferred
`)
}

func TestAnalyzeUFCSFreeFunctionAutorefRewrite(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_free_function_autoref.elisa", `
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

func TestAnalyzeUFCSFreeFunctionMatchesGenericRuntimeSurfaceReceiver(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_darray_view.elisa", `
def view[T](values: darray[T]) -> view[T]:
    return zeroed

def read(values: darray[i32]) -> usize:
    return values.view().len
`)

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl := funcSym.Node.(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	field, ok := ret.Value.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected return field expr, got %T", ret.Value)
	}
	call, ok := field.Object.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected rewritten UFCS call as field receiver, got %T", field.Object)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "view" {
		t.Fatalf("expected UFCS callee view, got %T %#v", call.Func, call.Func)
	}
	if len(call.LoweredArgs()) != 1 {
		t.Fatalf("expected receiver arg only, got %d", len(call.LoweredArgs()))
	}
	if receiver, ok := call.LoweredArgs()[0].(*ast.Ident); !ok || receiver.Name != "values" {
		t.Fatalf("expected inserted receiver arg values, got %T %#v", call.LoweredArgs()[0], call.LoweredArgs()[0])
	}
}

func TestAnalyzeUFCSRuntimeReceiverMethodsOnGenericStructs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_runtime_receiver_methods.elisa", `
struct FixtureSymbol:
    value: mutable i32

struct RegistryNamespace:
    marker: u8

struct Registry[K, T]:
    marker: u8

extern RegistrySlot
type RegistryId = id[RegistrySlot]

global registry: RegistryNamespace = RegistryNamespace{marker: 0}

def new[K, T](api: RegistryNamespace, owner: mutable Arena&) -> Registry[K, T]:
    _ = api
    _ = owner
    return zeroed

def declare[K, T](table: mutable Registry[K, T]&, key: K, value: T) -> RegistryId:
    _ = table
    _ = key
    _ = value
    return zeroed

def lookup[K, T](table: Registry[K, T]&, key: K) -> T?:
    _ = table
    _ = key
    return null

def update[K, T](table: mutable Registry[K, T]&, item_id: RegistryId, value: T) -> bool:
    _ = table
    _ = item_id
    _ = value
    return true

def get[K, T](table: Registry[K, T]&, item_id: RegistryId) -> T:
    _ = table
    _ = item_id
    return zeroed

def read(a: mutable Arena&) -> i32:
    can Global.Read, Memory.Allocate, Abort.Panic:
        table: mutable Registry[cstr[key_shape], FixtureSymbol] = registry.new(a)
        item_id: RegistryId = table.declare("alpha", FixtureSymbol{value: 7})
        if table.lookup("alpha") is found:
            _ = table.update(item_id, found)
        return table.get(item_id).value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl := funcSym.Node.(*ast.FuncDecl)
	if len(decl.Body) == 0 {
		t.Fatal("expected function body")
	}
	body := decl.Body
	if canStmt, ok := decl.Body[0].(*ast.CanStmt); ok {
		body = canStmt.Body
	}
	var ifStmt *ast.IfStmt
	var ret *ast.ReturnStmt
	for _, stmt := range body {
		if ifStmt == nil {
			if candidate, ok := stmt.(*ast.IfStmt); ok {
				ifStmt = candidate
			}
		}
		if ret == nil {
			if candidate, ok := stmt.(*ast.ReturnStmt); ok {
				ret = candidate
			}
		}
	}
	if ifStmt == nil {
		t.Fatalf("expected if stmt in body, got %#v", body)
	}
	if ret == nil {
		t.Fatalf("expected return stmt in body, got %#v", body)
	}
	ifLet, ok := ifStmt.Cond.(*ast.OptionalBindExpr)
	if !ok {
		t.Fatalf("expected optional bind cond, got %T", ifStmt.Cond)
	}
	lookupCall, ok := ifLet.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected lookup call, got %T", ifLet.Value)
	}
	lookupCallee, ok := lookupCall.Func.(*ast.Ident)
	if !ok || lookupCallee.Name != "lookup" {
		t.Fatalf("expected UFCS lookup callee, got %T %#v", lookupCall.Func, lookupCall.Func)
	}
	if len(lookupCall.LoweredArgs()) != 2 {
		t.Fatalf("expected receiver plus key arg, got %d", len(lookupCall.LoweredArgs()))
	}
	var updateCall *ast.CallExpr
	switch stmt := ifStmt.Then[0].(type) {
	case *ast.ExprStmt:
		updateCall = stmt.Expr.(*ast.CallExpr)
	case *ast.DiscardStmt:
		updateCall = stmt.Value.(*ast.CallExpr)
	default:
		t.Fatalf("expected update call statement, got %T", ifStmt.Then[0])
	}
	updateCallee, ok := updateCall.Func.(*ast.Ident)
	if !ok || updateCallee.Name != "update" {
		t.Fatalf("expected UFCS update callee, got %T %#v", updateCall.Func, updateCall.Func)
	}
	if len(updateCall.LoweredArgs()) != 3 {
		t.Fatalf("expected receiver plus two args, got %d", len(updateCall.LoweredArgs()))
	}
	field, ok := ret.Value.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field expr return, got %T", ret.Value)
	}
	getCall, ok := field.Object.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected get call in return, got %T", field.Object)
	}
	getCallee, ok := getCall.Func.(*ast.Ident)
	if !ok || getCallee.Name != "get" {
		t.Fatalf("expected UFCS get callee, got %T %#v", getCall.Func, getCall.Func)
	}
	if len(getCall.LoweredArgs()) != 2 {
		t.Fatalf("expected receiver plus symbol id arg, got %d", len(getCall.LoweredArgs()))
	}
}

func TestAnalyzeUFCSRuntimeReceiverMethodsOnIndexMaps(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_runtime_indexmap_methods.elisa", `
struct FixtureSymbol:
    value: mutable i32

struct IndexMapNamespace:
    marker: u8

struct IndexMap[K, T]:
    marker: u8

global indexmap: IndexMapNamespace = IndexMapNamespace{marker: 0}

def new[K, T](api: IndexMapNamespace, owner: mutable Arena&) -> IndexMap[K, T]:
    _ = api
    _ = owner
    return zeroed

def set[K, T](map: mutable IndexMap[K, T]&, key: K, value: T) -> usize:
    _ = map
    _ = key
    _ = value
    return 0

def get[K, T](map: IndexMap[K, T]&, key: K) -> T?:
    _ = map
    _ = key
    return null

def has[K, T](map: IndexMap[K, T]&, key: K) -> bool:
    _ = map
    _ = key
    return false

def count[K, T](map: IndexMap[K, T]&) -> usize:
    _ = map
    return 0

def read(a: mutable Arena&) -> i32:
    can Global.Read, Memory.Allocate, Abort.Panic:
        map: mutable IndexMap[cstr[key_shape], FixtureSymbol] = indexmap.new(a)
        _ = map.set("alpha", FixtureSymbol{value: 7})
        if map.has("alpha"):
            if map.get("alpha") is found:
                return found.value + map.count().i32()
        return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}

	funcSym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	decl := funcSym.Node.(*ast.FuncDecl)
	body := decl.Body
	if canStmt, ok := decl.Body[0].(*ast.CanStmt); ok {
		body = canStmt.Body
	}
	discard := body[1].(*ast.DiscardStmt)
	setCall := discard.Value.(*ast.CallExpr)
	setCallee, ok := setCall.Func.(*ast.Ident)
	if !ok || setCallee.Name != "set" {
		t.Fatalf("expected UFCS set callee, got %T %#v", setCall.Func, setCall.Func)
	}
	if len(setCall.LoweredArgs()) != 3 {
		t.Fatalf("expected receiver plus key/value args, got %d", len(setCall.LoweredArgs()))
	}
	outerIf := body[2].(*ast.IfStmt)
	hasCall := outerIf.Cond.(*ast.CallExpr)
	hasCallee, ok := hasCall.Func.(*ast.Ident)
	if !ok || hasCallee.Name != "has" {
		t.Fatalf("expected UFCS has callee, got %T %#v", hasCall.Func, hasCall.Func)
	}
	innerIf := outerIf.Then[0].(*ast.IfStmt)
	ifLet := innerIf.Cond.(*ast.OptionalBindExpr)
	getCall := ifLet.Value.(*ast.CallExpr)
	getCallee, ok := getCall.Func.(*ast.Ident)
	if !ok || getCallee.Name != "get" {
		t.Fatalf("expected UFCS get callee, got %T %#v", getCall.Func, getCall.Func)
	}
	ret := innerIf.Then[0].(*ast.ReturnStmt)
	binary := ret.Value.(*ast.BinaryExpr)
	countCall := binary.Right.(*ast.CastExpr).Operand.(*ast.CallExpr)
	countCallee, ok := countCall.Func.(*ast.Ident)
	if !ok || countCallee.Name != "count" {
		t.Fatalf("expected UFCS count callee, got %T %#v", countCall.Func, countCall.Func)
	}
}

func TestAnalyzeUFCSPrefersGenericStructFieldOverSameNamedUFCSFunction(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "ufcs_generic_field_precedence.elisa", `
struct Cell[T]:
    value: T

struct IndexMap[K, T]:
    marker: u8

def value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    _ = map
    _ = index
    return zeroed

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

def entry[K, T](map: IndexMap[K, T]&, index: usize) -> Entry[T]:
    _ = map
    _ = index
    return zeroed

def value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    _ = map
    _ = index
    return zeroed

def read_value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    return entry[K, T](map, index).value
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

def value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    _ = map
    _ = index
    return zeroed

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

def value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    _ = map
    _ = index
    return zeroed

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

def value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    _ = map
    _ = index
    return zeroed

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

def value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    _ = map
    _ = index
    return zeroed

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


def value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    _ = map
    _ = index
    return zeroed

def read[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    return map.value(index)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeMethodFunctionDoesNotShadowTupleValueBindings(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "method_tuple_value_binding.elisa", `
struct IndexMap[K, T]:
    marker: u8


def value[K, T](map: IndexMap[K, T]&, index: usize) -> T:
    _ = map
    _ = index
    return zeroed

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


def value[K, T](table: Registry[K, T]&, item_id: RegistryId) -> T:
    _ = table
    _ = item_id
    return zeroed


def entry[K, T](table: Registry[K, T]&, item_id: RegistryId) -> SymbolEntry[T]:
    _ = table
    _ = item_id
    return zeroed

def read[T](table: Registry[cstr, T]&, item_id: RegistryId) -> T:
    _ = table.entry(item_id)
    return table.value(item_id)
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
