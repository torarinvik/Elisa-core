package semantic

import (
	"reflect"
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
	a := &Analyzer{
		storeFlowDarrays: map[string]bool{"target": true},
		ufcsFunctionsByName: map[string][]*Symbol{
			// This overload cannot receive a DArray; it must not force the builtin
			// call onto the unknown-call fallback path.
			"Numbers.push": {{Name: "Numbers.push", Type: &FuncType{Params: []Type{
				&BuiltinType{Name: "i64"}, &BuiltinType{Name: "u32"},
			}}}},
		},
	}
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

	// An applicable generic UFCS receiver could shadow the builtin, so keep it
	// conservative even when the receiver is known to be a DArray.
	generic := &Analyzer{
		storeFlowDarrays: map[string]bool{"target": true},
		ufcsFunctionsByName: map[string][]*Symbol{
			"Generic.push": {{Name: "Generic.push", Type: &FuncType{Params: []Type{
				&RefType{Elem: &TypeParamType{Name: "T"}, Mutable: true}, &TypeParamType{Name: "U"},
			}}}},
		},
	}
	if generic.storeFlowBuiltinDarrayGrowth(call.Func.(*ast.FieldExpr)) {
		t.Fatal("a generic UFCS receiver can accept a DArray and must stay unresolved")
	}
	darrayUFCS := &Analyzer{
		storeFlowDarrays: map[string]bool{"target": true},
		ufcsFunctionsByName: map[string][]*Symbol{
			"DArray.push": {{Name: "DArray.push", Type: &FuncType{Params: []Type{
				&RefType{Elem: &DArrayType{Elem: &BuiltinType{Name: "u32"}}, Mutable: true}, &BuiltinType{Name: "u32"},
			}}}},
		},
	}
	if _, handled := darrayUFCS.applyCallStoreFlow(call, &storeFlowCtx{}, map[*ast.CallExpr][]string{}); handled {
		t.Fatal("a DArray-compatible UFCS receiver must keep the call conservative")
	}
	for _, receiver := range []Type{
		&TypeParamType{Name: "T"},
		&DArrayType{Elem: &BuiltinType{Name: "u32"}},
		&GenericInstanceType{Name: "DynArray", Base: &StructType{Name: "DynArray"}, Args: []Type{&BuiltinType{Name: "u32"}}},
		&RefType{Elem: &TypeParamType{Name: "T"}, Mutable: true},
	} {
		if !storeFlowUFCSMayAcceptDarrayReceiver(receiver) {
			t.Fatalf("receiver representation %T may accept a DArray and must remain conservative", receiver)
		}
	}
	for _, receiver := range []Type{
		&ViewType{Elem: &BuiltinType{Name: "u32"}},
		&GenericInstanceType{Name: "InlineVec", Base: &StructType{Name: "InlineVec"}, Args: []Type{&BuiltinType{Name: "u32"}}},
		&StructType{Name: "Container"},
	} {
		if storeFlowUFCSMayAcceptDarrayReceiver(receiver) {
			t.Fatalf("concrete nominal receiver %T cannot accept a builtin DArray", receiver)
		}
	}
	inlineVecUFCS := &Analyzer{
		storeFlowDarrays: map[string]bool{"target": true},
		ufcsFunctionsByName: map[string][]*Symbol{
			"InlineVec.push": {{Name: "InlineVec.push", Type: &FuncType{Params: []Type{
				&RefType{Elem: &GenericInstanceType{Name: "InlineVec", Base: &StructType{Name: "InlineVec"}, Args: []Type{&TypeParamType{Name: "T"}}}, Mutable: true},
				&TypeParamType{Name: "T"},
			}}}},
		},
	}
	if !inlineVecUFCS.storeFlowBuiltinDarrayGrowth(call.Func.(*ast.FieldExpr)) {
		t.Fatal("an unrelated generic-struct UFCS push must not make builtin DArray push unresolved")
	}

	// A same-spelled method on an unverified receiver stays on the conservative path.
	unknown := &Analyzer{}
	if _, handled := unknown.applyCallStoreFlow(call, &storeFlowCtx{}, map[*ast.CallExpr][]string{}); handled {
		t.Fatal("an unverified .push method must remain conservative")
	}
}

func TestStoreFlowCallFitsBorrowingDefaultArguments(t *testing.T) {
	src := `def reader(name: sview, owner_hint: sview = "") -> u32:
    return name.len.u32() + owner_hint.len.u32()

def caller(name: sview) -> u32:
    return reader(name)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_borrowing_default.elisa", src, AnalyzeOptions{})
	var reader *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "reader" {
			reader = candidate
			break
		}
	}
	if reader == nil {
		t.Fatal("reader was not parsed")
	}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "reader"}, Args: []ast.Expr{&ast.Ident{Name: "name"}}}
	if !storeFlowCallFitsParams(reader, call) {
		t.Fatal("a declaration-owned borrowed default must not make a call unresolved for StoreFlow")
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

// Compiler helpers may read region-bearing AST buffers while mutating a pointer-free
// compiler table. The table mutation alone must not make the AST buffer look retained.
// This models the `Expr[]` -> `ValueType` inference path used by Stage1's try lowering.
func TestStoreFlowSummaryDoesNotRetainASTArgsThroughPointerFreeTableMutation(t *testing.T) {
	src := `struct Expr:
    text: sview
struct ValueType:
    kind: u32
struct StructTable:
    visits: u32

def infer_value(arguments: darray[Expr]&, structs: mutable StructTable&) -> ValueType:
    item: Expr = arguments[0]
    structs.visits <- structs.visits + item.text.len.u32()
    return ValueType{kind: item.text.len.u32()}

def infer_value_through_helper(arguments: darray[Expr]&, structs: mutable StructTable&) -> ValueType:
    return infer_value(arguments, structs)

def caller(structs: mutable StructTable&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        region short(4096):
            bytes: mutable darray[u8] @short = [65]
            arguments: mutable darray[Expr] = [Expr{text: bytes.as_sview()}]
            _ = infer_value_through_helper(arguments, structs)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_ast_args_pointer_free_table.elisa", src, AnalyzeOptions{})
	for _, diagnostic := range result.Errors() {
		if strings.Contains(diagnostic, "call may store a reference to function-local storage") {
			t.Fatalf("reading AST args to update a pointer-free table must not retain them, got: %s", diagnostic)
		}
	}

	var helper *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "infer_value" {
			helper = fn
			break
		}
	}
	if helper == nil {
		t.Fatal("test helper was not parsed")
	}
	summary := result.analyzer.storeFlowSummaryFor(helper)
	if summary == nil {
		t.Fatal("pointer-free table mutation should have a settled StoreFlow summary")
	}
	if !summary.writtenParam(1) {
		t.Fatal("test must model mutation through the compiler table parameter")
	}
	if summary.reaches(0, 1) {
		t.Fatalf("AST argument storage must not flow into the pointer-free compiler table: reaches(0,1), conn=%v reach=%v ret=%v written=%v", summary.conn, summary.reach, summary.retConn, summary.written)
	}
}

func TestStoreFlowSummaryDoesNotRetainASTArgsThroughTypeInferenceState(t *testing.T) {
	src := `struct Expr:
    text: sview
struct ValueType:
    kind: u32
    bits: u32
struct Scope:
    visits: u32
struct FnTable:
    visits: u32
struct StructTable:
    visits: mutable darray[u32]
struct GenericTable:
    visits: mutable darray[u32]

def infer_generic_args(arguments: darray[Expr]&, local_scope: mutable Scope&, fns: FnTable&, structs: mutable StructTable&, generics: mutable GenericTable&) -> ValueType:
    kind: u32 = arguments[0].text.len.u32()
    local_scope.visits <- local_scope.visits + kind
    structs.visits[0] <- structs.visits[0] + kind
    generics.visits[0] <- generics.visits[0] + kind
    return ValueType{kind: kind, bits: fns.visits}

def generic_return_type_at(inferred: darray[ValueType]&, structs: mutable StructTable&, generics: mutable GenericTable&) -> ValueType:
    structs.visits[0] <- structs.visits[0] + inferred[0].kind
    generics.visits[0] <- generics.visits[0] + inferred[0].bits
    return inferred[0]

def error_callee_value_type(arguments: darray[Expr]&, local_scope: mutable Scope&, fns: FnTable&, structs: mutable StructTable&, generics: mutable GenericTable&) -> ValueType:
    inferred: mutable darray[ValueType] = [infer_generic_args(arguments, local_scope, fns, structs, generics)]
    return generic_return_type_at(inferred, structs, generics)

def caller(arguments: darray[Expr]&, local_scope: mutable Scope&, fns: FnTable&, structs: mutable StructTable&, generics: mutable GenericTable&) -> ValueType:
    return error_callee_value_type(arguments, local_scope, fns, structs, generics)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_generic_type_inference_state.elisa", src, AnalyzeOptions{})
	var helper *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "error_callee_value_type" {
			helper = fn
			break
		}
	}
	if helper == nil {
		t.Fatal("test helper was not parsed")
	}
	summary := result.analyzer.storeFlowSummaryFor(helper)
	if summary == nil {
		t.Fatal("generic type-inference helper should have a settled StoreFlow summary")
	}
	if !summary.writtenParam(1) || !summary.writtenParam(3) || !summary.writtenParam(4) {
		t.Fatalf("test must model mutation through Scope, StructTable and GenericTable: %v", summary.written)
	}
	for _, state := range []int{1, 3, 4} {
		if summary.reaches(0, state) {
			t.Fatalf("type inference from AST arguments must not retain their storage in state parameter %d: reach=%v conn=%v ret=%v written=%v", state, summary.reach, summary.conn, summary.retConn, summary.written)
		}
	}
	for _, diagnostic := range result.Errors() {
		if strings.Contains(diagnostic, "call may store a reference to function-local storage") {
			t.Fatalf("pointer-free inferred type metadata must not retain AST argument storage: %s", diagnostic)
		}
	}
}

func TestStoreFlowSummaryRejectsASTBorrowStoredByTypeInference(t *testing.T) {
	src := `struct Expr:
    text: sview
struct ValueType:
    name: sview
struct Metadata:
    last_name: mutable sview

def infer_generic_args(arguments: darray[Expr]&) -> ValueType:
    return ValueType{name: arguments[0].text}

def generic_return_type_at(inferred: darray[ValueType]&, metadata: mutable Metadata&) -> ValueType:
    metadata.last_name <- inferred[0].name
    return inferred[0]

def error_callee_value_type(arguments: darray[Expr]&, metadata: mutable Metadata&) -> ValueType:
    inferred: mutable darray[ValueType] = [infer_generic_args(arguments)]
    return generic_return_type_at(inferred, metadata)

def caller(metadata: mutable Metadata&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        region short(4096):
            bytes: mutable darray[u8] @short = [65]
            arguments: mutable darray[Expr] = [Expr{text: bytes.as_sview()}]
            _ = error_callee_value_type(arguments, metadata)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_type_inference_real_borrow.elisa", src, AnalyzeOptions{})
	var helper *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "error_callee_value_type" {
			helper = fn
			break
		}
	}
	if helper == nil {
		t.Fatal("test helper was not parsed")
	}
	summary := result.analyzer.storeFlowSummaryFor(helper)
	if summary == nil || !summary.reaches(0, 1) {
		t.Fatalf("a source view stored through generic type inference must reach Metadata: summary=%#v", summary)
	}
}

func TestStoreFlowSummaryDoesNotTreatMutableReadOnlyParamsAsWrites(t *testing.T) {
	src := `struct Expr:
    text: sview
struct Metadata:
    count: u32

def inspect(expressions: darray[Expr]&, metadata: mutable Metadata&) -> u32:
    return expressions[0].text.len.u32() + metadata.count

def caller(expressions: darray[Expr]&, metadata: mutable Metadata&) -> u32:
    return inspect(expressions, metadata)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_mutable_read_only_params.elisa", src, AnalyzeOptions{})
	for _, name := range []string{"inspect", "caller"} {
		var fn *ast.FuncDecl
		for _, decl := range result.File.Decls {
			if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == name {
				fn = candidate
				break
			}
		}
		if fn == nil {
			t.Fatalf("function %q was not parsed", name)
		}
		summary := result.analyzer.storeFlowSummaryFor(fn)
		if summary == nil {
			t.Fatalf("read-only mutable-parameter function %q should have a settled summary", name)
		}
		if summary.writtenParam(0) || summary.writtenParam(1) {
			t.Fatalf("read-only mutable parameters in %q must not be treated as writes: %v", name, summary.written)
		}
		if summary.reaches(0, 1) || summary.reaches(1, 0) {
			t.Fatalf("reading a borrowed value and metadata must not imply storage between them in %q: reach=%v", name, summary.reach)
		}
	}
}

func TestStoreFlowSummaryDoesNotRetainASTArgsThroughIndexedPointerFreeMutation(t *testing.T) {
	src := `struct Expr:
    text: sview
struct StructTable:
    visits: mutable darray[u32]

def infer_value(arguments: darray[Expr]&, structs: mutable StructTable&, slot: usize) -> u32:
    item: Expr = arguments[0]
    structs.visits[slot] <- item.text.len.u32()
    return item.text.len.u32()

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_indexed_pointer_free_table.elisa", src, AnalyzeOptions{})
	var helper *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "infer_value" {
			helper = fn
			break
		}
	}
	if helper == nil {
		t.Fatal("test helper was not parsed")
	}
	summary := result.analyzer.storeFlowSummaryFor(helper)
	if summary == nil {
		t.Fatal("indexed pointer-free table mutation should have a settled StoreFlow summary")
	}
	if !summary.writtenParam(1) {
		t.Fatal("test must model mutation through the compiler table parameter")
	}
	if summary.reaches(0, 1) {
		t.Fatalf("AST argument storage must not flow into an indexed pointer-free table: reaches(0,1), conn=%v reach=%v ret=%v written=%v", summary.conn, summary.reach, summary.retConn, summary.written)
	}
}

func TestStoreFlowSummaryUsesColdBinderTypeForIndexedPointerFreeMutation(t *testing.T) {
	src := `struct Expr:
    text: sview
struct ValueType:
    length: u32

def store_value(arguments: darray[Expr]&, values: mutable darray[ValueType]&, slot: usize, depth: u32) -> void:
    if depth > 0:
        store_value(arguments, values, slot, depth - 1)
    values[slot] <- ValueType{length: arguments[0].text.len.u32()}

def store_flag(arguments: darray[Expr]&, values: mutable darray[bool]&, slot: usize, depth: u32) -> void:
    if depth > 0:
        store_flag(arguments, values, slot, depth - 1)
    values[slot] <- arguments[0].text.len > 0

def store_view(arguments: darray[Expr]&, values: mutable darray[sview]&, slot: usize, depth: u32) -> void:
    if depth > 0:
        store_view(arguments, values, slot, depth - 1)
    values[slot] <- arguments[0].text

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_cold_index_type.elisa", src, AnalyzeOptions{})
	find := func(name string) *ast.FuncDecl {
		for _, decl := range result.File.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == name {
				return fn
			}
		}
		return nil
	}
	value := find("store_value")
	flag := find("store_flag")
	view := find("store_view")
	if value == nil || flag == nil || view == nil {
		t.Fatal("test helpers were not parsed")
	}
	findIndexedTarget := func(fn *ast.FuncDecl) ast.Expr {
		var target ast.Expr
		var walk func(reflect.Value)
		walk = func(value reflect.Value) {
			if !value.IsValid() || target != nil {
				return
			}
			switch value.Kind() {
			case reflect.Interface, reflect.Pointer:
				if !value.IsNil() {
					walk(value.Elem())
				}
			case reflect.Struct:
				if value.CanInterface() {
					if assign, ok := value.Addr().Interface().(*ast.AssignStmt); ok {
						if index, ok := assign.Target.(*ast.IndexExpr); ok {
							if base, ok := index.Object.(*ast.Ident); ok && base.Name == "values" {
								target = index
								return
							}
						}
					}
				}
				for i := 0; i < value.NumField(); i++ {
					walk(value.Field(i))
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < value.Len(); i++ {
					walk(value.Index(i))
				}
			}
		}
		walk(reflect.ValueOf(fn.Body))
		return target
	}
	forceColdIndexTarget := func(fn *ast.FuncDecl) {
		target := findIndexedTarget(fn)
		if target == nil {
			t.Fatalf("%s indexed mutation target was not found", fn.Name)
		}
		delete(result.analyzer.exprTypes, target)
		if index, ok := target.(*ast.IndexExpr); ok {
			delete(result.analyzer.exprTypes, index.Object)
		}
		result.analyzer.storeFlowSummaries = nil
		result.analyzer.storeFlowInProgress = nil
		result.analyzer.storeFlowActive = nil
		return
	}
	valueTarget := findIndexedTarget(value)
	forceColdIndexTarget(value)
	result.analyzer.storeFlowCurrentDecl = value
	if !result.analyzer.storeFlowPointerFreePlace(valueTarget) {
		t.Fatal("the cold darray[ValueType] indexed place must prove pointer-free without cached place types")
	}
	valueSummary := result.analyzer.storeFlowSummaryFor(value)
	if valueSummary == nil {
		t.Fatal("pointer-free indexed StoreFlow should have a settled recursive summary")
	}
	if valueSummary.reaches(0, 1) {
		t.Fatalf("a cold darray[ValueType] index target must not retain the AST argument; reach=%v", valueSummary.reach)
	}
	flagTarget := findIndexedTarget(flag)
	forceColdIndexTarget(flag)
	result.analyzer.storeFlowCurrentDecl = flag
	if !result.analyzer.storeFlowPointerFreePlace(flagTarget) {
		t.Fatal("the cold darray[bool] indexed place must prove pointer-free without cached place types")
	}
	flagSummary := result.analyzer.storeFlowSummaryFor(flag)
	if flagSummary == nil || flagSummary.reaches(0, 1) {
		t.Fatalf("a cold darray[bool] index target must not retain the AST argument; summary=%#v", flagSummary)
	}
	viewTarget := findIndexedTarget(view)
	forceColdIndexTarget(view)
	result.analyzer.storeFlowCurrentDecl = view
	if result.analyzer.storeFlowPointerFreePlace(viewTarget) {
		t.Fatal("the cold darray[sview] indexed place must remain pointerful without cached place types")
	}
	viewSummary := result.analyzer.storeFlowSummaryFor(view)
	if viewSummary == nil || !viewSummary.reaches(0, 1) {
		t.Fatalf("a cold darray[sview] index target must retain the real borrow flow; summary=%#v", viewSummary)
	}
}

func TestStoreFlowSummaryStillTracksBorrowWrittenIntoIndexedPointerfulField(t *testing.T) {
	src := `struct Expr:
    text: sview
struct StructTable:
    texts: mutable darray[sview]

def store_text(arguments: darray[Expr]&, structs: mutable StructTable&, slot: usize) -> void:
    structs.texts[slot] <- arguments[0].text

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_indexed_pointerful_table.elisa", src, AnalyzeOptions{})
	var helper *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "store_text" {
			helper = fn
			break
		}
	}
	if helper == nil {
		t.Fatal("test helper was not parsed")
	}
	summary := result.analyzer.storeFlowSummaryFor(helper)
	if summary == nil {
		t.Fatal("indexed pointerful field write should have a settled StoreFlow summary")
	}
	if !summary.reaches(0, 1) {
		t.Fatal("view stored into an indexed table element must remain a source-to-container flow")
	}
}

func TestStoreFlowSummaryStillTracksBorrowWrittenIntoPointerfulField(t *testing.T) {
	src := `struct Expr:
    text: sview
struct StructTable:
    last_text: mutable sview

def store_text(arguments: darray[Expr]&, structs: mutable StructTable&) -> void:
    structs.last_text <- arguments[0].text

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "store_flow_pointerful_field.elisa", src)
	var helper *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "store_text" {
			helper = fn
			break
		}
	}
	if helper == nil {
		t.Fatal("test helper was not parsed")
	}
	summary := result.analyzer.storeFlowSummaryFor(helper)
	if summary == nil {
		t.Fatal("pointerful field write should have a settled StoreFlow summary")
	}
	if !summary.reaches(0, 1) {
		t.Fatal("view stored into the table field must remain a source-to-container flow")
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
