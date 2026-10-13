package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

const storeFlowPrelude = `enum Expr:
    Leaf(name: sview)
    Call(callee: sview, names: darray[sview])
    Absent

`

func TestStoreFlowBuiltinDarrayPushKeepsOneWayStorageAndResultFlow(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.FieldExpr{Object: &ast.Ident{Name: "target"}, Field: "push"},
		Args: []ast.Expr{&ast.Ident{Name: "source"}},
	}
	a := &Analyzer{storeFlowDarrays: map[string]bool{"target": true}}
	c := &storeFlowCtx{written: map[string]bool{"target": true}, scalars: map[string]bool{}}
	retNames, handled := a.applyCallStoreFlow(call, c, map[*ast.CallExpr][]string{})
	if !handled {
		t.Fatal("a proven builtin DArray push should have a precise store-flow summary")
	}
	if len(c.edges) != 1 || c.edges[0] != [2]string{"source", "target"} {
		t.Fatalf("push must flow element contents into the receiver only, got edges %v", c.edges)
	}
	if len(retNames) != 2 || retNames[0] != "target" || retNames[1] != "source" {
		t.Fatalf("push result must depend on receiver and inserted element, got %v", retNames)
	}

	// A same-spelled method on an unverified receiver stays on the conservative path.
	unknown := &Analyzer{}
	if _, handled := unknown.applyCallStoreFlow(call, &storeFlowCtx{}, map[*ast.CallExpr][]string{}); handled {
		t.Fatal("an unverified .push method must remain conservative")
	}
}

func TestStoreFlowBuiltinDarrayTruncateDoesNotStoreItsCount(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.FieldExpr{Object: &ast.Ident{Name: "bindings"}, Field: "truncate"},
		Args: []ast.Expr{&ast.IntLit{Value: "0"}},
	}
	a := &Analyzer{storeFlowDarrays: map[string]bool{"bindings": true}}
	c := &storeFlowCtx{written: map[string]bool{"bindings": true}, scalars: map[string]bool{}}
	retNames, handled := a.applyCallStoreFlow(call, c, map[*ast.CallExpr][]string{})
	if !handled {
		t.Fatal("a proven builtin DArray truncate should have a precise store-flow summary")
	}
	if len(c.edges) != 0 {
		t.Fatalf("truncate must not store its integer count in the DArray, got edges %v", c.edges)
	}
	if len(retNames) != 1 || retNames[0] != "bindings" {
		t.Fatalf("truncate result must retain receiver identity, got %v", retNames)
	}

	written := map[string]bool{}
	a.storeFlowMarkCall(call, written, func(expr ast.Expr) {
		if id, ok := expr.(*ast.Ident); ok && id != nil {
			written[id.Name] = true
		}
	})
	if !written["bindings"] {
		t.Fatal("truncate must continue to mark its DArray receiver mutated")
	}
}

func TestStoreFlowDarrayTruncateLeavesPotentialUFCSMethodsUnresolved(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.FieldExpr{Object: &ast.Ident{Name: "bindings"}, Field: "truncate"},
		Args: []ast.Expr{&ast.IntLit{Value: "0"}},
	}
	a := &Analyzer{
		storeFlowDarrays: map[string]bool{"bindings": true},
		ufcsFunctionsByName: map[string][]*Symbol{
			"Custom.truncate":  {{Type: &FuncType{Params: []Type{&DArrayType{}}}}},
			"Generic.truncate": {{Type: &FuncType{TypeParams: []string{"T"}, Params: []Type{&TypeParamType{Name: "T"}}}}},
		},
	}
	if a.storeFlowBuiltinDarrayTruncate(call) {
		t.Fatal("a visible UFCS truncate that accepts DArray must stay on the conservative path")
	}
	if _, handled := a.applyCallStoreFlow(call, &storeFlowCtx{}, map[*ast.CallExpr][]string{}); handled {
		t.Fatal("a possibly user-defined truncate call must not receive the builtin summary")
	}
}

func TestStoreFlowDarrayTruncateRequiresIntegerCount(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.FieldExpr{Object: &ast.Ident{Name: "bindings"}, Field: "truncate"},
		Args: []ast.Expr{&ast.StringLit{Value: "0"}},
	}
	a := &Analyzer{storeFlowDarrays: map[string]bool{"bindings": true}}
	if a.storeFlowBuiltinDarrayTruncate(call) {
		t.Fatal("a noninteger truncate count must not receive the builtin summary")
	}
}

func TestStoreFlowEnumVariantConstructorReturnsOnlyPayloadFlow(t *testing.T) {
	variant := &EnumVariant{Name: "Ident", Payload: []Type{&SViewType{}, &BuiltinType{Name: "usize"}}}
	enumType := &EnumType{Name: "Expr", Variants: []*EnumVariant{variant}, VariantMap: map[string]*EnumVariant{"Ident": variant}}
	call := &ast.CallExpr{
		Func: &ast.FieldExpr{Object: &ast.Ident{Name: "Expr"}, Field: "Ident"},
		Args: []ast.Expr{&ast.Ident{Name: "name"}, &ast.Ident{Name: "line"}},
	}
	a := &Analyzer{namedTypes: map[string]Type{"Expr": enumType}}
	c := &storeFlowCtx{written: map[string]bool{}, scalars: map[string]bool{"line": true}}
	retNames, handled := a.applyCallStoreFlow(call, c, map[*ast.CallExpr][]string{})
	if !handled {
		t.Fatal("a visible enum variant constructor should have a precise store-flow summary")
	}
	if len(retNames) != 1 || retNames[0] != "name" {
		t.Fatalf("enum result should retain only region-bearing payloads, got %v", retNames)
	}
	if len(c.edges) != 0 {
		t.Fatalf("enum construction must not store payloads into unrelated state, got edges %v", c.edges)
	}
}

func TestStoreFlowEnumVariantConstructorDoesNotIgnoreValueShadowing(t *testing.T) {
	variant := &EnumVariant{Name: "Ident", Payload: []Type{&SViewType{}}}
	enumType := &EnumType{Name: "Expr", Variants: []*EnumVariant{variant}, VariantMap: map[string]*EnumVariant{"Ident": variant}}
	call := &ast.CallExpr{
		Func: &ast.FieldExpr{Object: &ast.Ident{Name: "Expr"}, Field: "Ident"},
		Args: []ast.Expr{&ast.Ident{Name: "name"}},
	}
	a := &Analyzer{
		namedTypes:           map[string]Type{"Expr": enumType},
		storeFlowCurrentDecl: &ast.FuncDecl{Params: []ast.ParamDecl{{Name: "Expr"}}},
	}
	if a.storeFlowEnumVariantConstructor(call) {
		t.Fatal("a value binding shadowing the enum name must keep field calls on the conservative path")
	}
}

// A self-recursive walker whose recursive call passes comprehension-built view containers next to a
// writable Expr container it never stores them into is accepted.
func TestStoreFlowSummaryRecursiveWalkerKeepsArgumentsApart(t *testing.T) {
	src := storeFlowPrelude + `def walk(depth: u32, nesting: darray[sview]&, locals: mutable darray[sview]&, exprs: mutable darray[Expr]&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        return if depth == 0
        exprs.push(Expr.Absent)
        child: mutable darray[sview] = [entry for entry in nesting]
        child.push("x")
        kid: mutable darray[sview] = [entry for entry in locals]
        walk(depth - 1, child, kid, exprs)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "store_flow_walker.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// A callee that really stores a view argument into the writable container stays rejected, also
// through a recursive hop.
func TestStoreFlowSummaryStillRejectsRealStore(t *testing.T) {
	src := storeFlowPrelude + `def put(depth: u32, first: Expr, exprs: mutable darray[Expr]&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        return if depth == 0
        exprs.push(first)
        put(depth - 1, first, exprs)

def run(exprs: mutable darray[Expr]&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        buf: mutable darray[u8] = []
        buf.push(65)
        put(2, Expr.Leaf(buf.as_sview()), exprs)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_real_store.elisa", src, AnalyzeOptions{})
	found := false
	for _, e := range result.Errors() {
		if strings.Contains(e, "call may store a value in region") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the store escape to be reported, got: %v", result.Errors())
	}
}

// By-value descriptors may be computed from a short-lived view and cached in a
// longer-lived context. The descriptor carries no borrow, so StoreFlow must not
// treat the input view as stored through a resolved named local.
func TestStoreFlowSummaryKeepsResolvedPointerFreeLocalsOutOfBorrowFlow(t *testing.T) {
	src := `struct ValueType:
    kind: usize
struct Context:
    cached: ValueType

def describe(name: sview) -> ValueType:
    return ValueType{kind: name.len}

def record(name: sview, context: mutable Context&) -> ValueType:
    value: mutable ValueType = describe(name)
    context.cached <- value
    return value

def caller() -> void can[Memory.Allocate, Abort.Panic]:
    context: mutable Context = Context{cached: ValueType{kind: 0}}
    region short(4096):
        bytes: mutable darray[u8] @short = [65]
        record(bytes.as_sview(), context)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_resolved_scalar_local.elisa", src, AnalyzeOptions{})
	for _, diagnostic := range result.Errors() {
		if strings.Contains(diagnostic, "call may store a value in region") {
			t.Fatalf("a by-value descriptor must not retain its source view, got: %s", diagnostic)
		}
	}
}

func TestStoreFlowNamedPointerFreeTypesRejectAmbiguousLeafNames(t *testing.T) {
	a := &Analyzer{namedTypes: map[string]Type{
		"Numeric.Position":  &BuiltinType{Name: "u32"},
		"Borrowed.Position": &SViewType{},
	}}
	if a.storeFlowPointerFreeTypeExpr(&ast.NamedType{Name: "Position"}, map[Type]bool{}) {
		t.Fatal("an unqualified type spelling with only unrelated module suffix matches must remain unknown")
	}
	if !a.storeFlowPointerFreeTypeExpr(&ast.NamedType{Name: "Numeric.Position"}, map[Type]bool{}) {
		t.Fatal("a qualified, unambiguous pointer-free type should still be classified")
	}
	if a.storeFlowPointerFreeTypeExpr(&ast.NamedType{Name: "Borrowed.Position"}, map[Type]bool{}) {
		t.Fatal("a qualified borrow-bearing type must not be classified as pointer-free")
	}
}

func TestStoreFlowPointerFreeTypeRejectsGenericParamShadowingConcreteName(t *testing.T) {
	a := &Analyzer{
		namedTypes:           map[string]Type{"T": &BuiltinType{Name: "u32"}},
		storeFlowCurrentDecl: &ast.FuncDecl{TypeParams: []string{"T"}},
	}
	if a.storeFlowPointerFreeTypeExpr(&ast.NamedType{Name: "T"}, map[Type]bool{}) {
		t.Fatal("a generic type parameter must remain unknown even when a concrete type shares its name")
	}
}

func TestStoreFlowGenericPointerLikeTypeParameterStillRejectsRealStore(t *testing.T) {
	src := `struct T:
    value: u32

def keep[T](value: T, dst: mutable darray[T]&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        dst.push(value)

def caller(dst: mutable darray[sview]&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        region short(4096):
            bytes: mutable darray[u8] @short = [65]
            keep(bytes.as_sview(), dst)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_generic_type_param_shadow.elisa", src, AnalyzeOptions{})
	for _, diagnostic := range result.Errors() {
		if strings.Contains(diagnostic, "call may store a value in region") {
			return
		}
	}
	t.Fatalf("a generic type parameter must retain real borrow flow despite a same-named root type, got: %v", result.Errors())
}

func TestStoreFlowEndsInReturnThroughSynthesizedRegion(t *testing.T) {
	body := []ast.Stmt{&ast.RegionStmt{Lazy: true, Body: []ast.Stmt{
		&ast.CanStmt{Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "result"}}}},
	}}}
	if !storeFlowEndsInReturn(body) {
		t.Fatal("transparent generated regions and permission blocks must not hide the function's explicit return")
	}
}

func TestStoreFlowEndsInReturnRejectsImplicitRegionTail(t *testing.T) {
	body := []ast.Stmt{&ast.RegionStmt{Lazy: true, Body: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.Ident{Name: "result"}},
	}}}
	if storeFlowEndsInReturn(body) {
		t.Fatal("an expression at the end of a region is not an explicit return")
	}
}

func TestStoreFlowModuleTypeShadowStillRejectsRealStore(t *testing.T) {
	src := `struct Position:
    index: u32

module Borrowed:
    struct Position:
        text: sview
    struct Context:
        cached: mutable Position

    def store(value: Position, items: mutable darray[Position]&) -> void can[Abort.Panic, Memory.Allocate]:
        can Abort.Panic, Memory.Allocate:
            items.push(value)

    def record(value: Position, context: mutable Context&) -> void:
        context.cached <- value

    def run() -> void can[Abort.Panic, Memory.Allocate]:
        can Abort.Panic, Memory.Allocate:
            context: mutable Context = Context{cached: Position{text: ""}}
            items: mutable darray[Position] = []
            region short(4096):
                bytes: mutable darray[u8] @short = [65]
                value: Position = Position{text: bytes.as_sview()}
                record(value, context)
                store(value, items)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_module_type_shadow.elisa", src, AnalyzeOptions{})
	for _, diagnostic := range result.Errors() {
		if strings.Contains(diagnostic, "call may store a value in region") {
			return
		}
	}
	t.Fatalf("a module-local borrow-bearing Position must retain its store-flow edge despite a root Position, got: %v", result.Errors())
}

// A connected parameter pair is not necessarily connected in both directions.
// If the callee copies from a longer-lived source into a shorter-lived target,
// the reverse-only pair at the call site cannot store the short-lived value
// back into the longer-lived source and must not be rejected.
func TestStoreFlowSummaryCallArgumentDirectionAvoidsReverseOnlyEscape(t *testing.T) {
	src := `enum E:
    Text(value: sview)

def append_source_into_target(target: mutable darray[E]&, source: mutable darray[E]&):
    can Memory.Allocate, Abort.Panic:
        target.push(source[0])

def caller():
    can Memory.Allocate, Abort.Panic:
        region outer(4096):
            source: mutable darray[E] @outer = [E.Text("static")]
            region inner(2048):
                target: mutable darray[E] @inner = []
                append_source_into_target(target, source)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_direction_reverse_only.elisa", src)
	if errs := strings.Join(result.Errors(), "\n"); errs != "" {
		t.Fatalf("reverse-only flow must not be treated as a source-to-target store, got:\n%s", errs)
	}
}
