package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"strings"
	"testing"
)

func TestStoreFlowSummaryDoesNotRetainSViewExtendedIntoByteDarray(t *testing.T) {
	src := `struct ValueType:
    text: sview

def copy_text(text: sview, bytes: mutable darray[u8]&) -> void:
    bytes.extend(text)

def copy_values(source: darray[ValueType]&, target: mutable darray[ValueType]&) -> void:
    target.extend(source)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_flow_sview_extend.elisa", src, AnalyzeOptions{})
	findSummary := func(name string) *storeFlowSummary {
		for _, decl := range result.File.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == name {
				return result.analyzer.storeFlowSummaryFor(fn)
			}
		}
		return nil
	}
	copyText := findSummary("copy_text")
	if copyText == nil {
		t.Fatal("sview-to-byte copy helper should have a settled StoreFlow summary")
	}
	if copyText.reaches(0, 1) {
		t.Fatalf("byte extension copies data and must not retain the source sview: reach=%v conn=%v written=%v", copyText.reach, copyText.conn, copyText.written)
	}
	copyValues := findSummary("copy_values")
	if copyValues == nil || !copyValues.reaches(0, 1) {
		t.Fatalf("extension of borrow-bearing elements must preserve source flow, summary=%+v", copyValues)
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
