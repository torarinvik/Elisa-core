package semantic_test

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestAnalyzeRejectsReusingConsumedThreadField(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64
extern detach(thread: Thread[i64, Joinable]) -> void

struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: mutable Holder) -> void:
    value: i64 = join(move holder.thread)
    _ = value
    detach(move holder.thread)
`
	_, errs := parseAndAnalyze(t, "consumed_thread_field_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "thread handle \"holder.thread\" cannot be used: usage facts were consumed by argument to call \"join\"") {
		t.Fatalf("expected consumed-thread-field diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsAddressOfAffineHandleField(t *testing.T) {
	src := `struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: mutable Holder) -> void:
    borrow: stack Thread[i64, Joinable]& = &holder.thread
    _ = borrow
`
	_, errs := parseAndAnalyze(t, "affine_handle_addr_of_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot take address of thread handle") {
		t.Fatalf("expected affine-address diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsAffineHandleGlobals(t *testing.T) {
	src := `struct Holder:
    thread: mutable Thread[i64, Joinable]

global current_thread: Thread[i64, Joinable] = zeroed
global current_holder: Holder = zeroed
extern foreign_task: Task[i64, Pending]
`
	_, errs := parseAndAnalyze(t, "affine_handle_globals_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "global \"current_thread\" cannot store linear handle values of type Thread[i64, Joinable]") {
		t.Fatalf("expected direct affine-global diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "global \"current_holder\" cannot store linear handle values of type Holder") {
		t.Fatalf("expected structural affine-global diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "extern var \"foreign_task\" cannot store linear handle values of type Task[i64, Pending]") {
		t.Fatalf("expected affine extern-global diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReferencesToAffineContainingValues(t *testing.T) {
	src := `struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad_param(holder: Holder&) -> void:
    pass

def bad_local(holder: Holder) -> void:
    alias: Holder& = &holder
    _ = alias
`
	_, errs := parseAndAnalyze(t, "affine_handle_refs_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "references to values containing linear handles are not supported; got Holder&") {
		t.Fatalf("expected affine-reference diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "cannot take address of value containing linear handles") {
		t.Fatalf("expected affine-address-of-container diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsCopyingAggregateAfterAffineFieldConsume(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

struct Holder:
    thread: mutable Thread[i64, Joinable]
    count: mutable i64

def bad(holder: Holder) -> void:
    value: i64 = join(move holder.thread)
    _ = value
    copy: Holder = holder
`
	_, errs := parseAndAnalyze(t, "affine_container_copy_after_field_move_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "value containing linear handles \"holder\" cannot be used: usage facts were consumed by argument to call \"join\"") {
		t.Fatalf("expected aggregate-after-field-move diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingMovedAffineContainingAggregate(t *testing.T) {
	src := `struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: Holder) -> void:
    copy: Holder = move holder
    _ = move holder
`
	_, errs := parseAndAnalyze(t, "affine_container_move_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "value containing linear handles \"holder\" cannot be used: usage facts were consumed by move into local \"copy\"") {
		t.Fatalf("expected moved-aggregate diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingAffineHandleAfterStructLiteralMove(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

struct Holder:
    thread: mutable Thread[i64, Joinable]
    count: mutable i64

def bad(thread: Thread[i64, Joinable]) -> i64:
    holder: Holder = Holder{thread: move thread, count: 1}
    return join(move thread)
`
	_, errs := parseAndAnalyze(t, "affine_struct_literal_move_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"thread\" cannot be used: usage facts were consumed by move into struct literal field \"thread\"") {
		t.Fatalf("expected struct-literal move diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingAffineHandleAfterArrayLiteralMove(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def bad(thread: Thread[i64, Joinable]) -> i64:
    items: array[Thread[i64, Joinable], 1] = [move thread]
    return join(move thread)
`
	_, errs := parseAndAnalyze(t, "affine_array_literal_move_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"thread\" cannot be used: usage facts were consumed by move into array literal element") {
		t.Fatalf("expected array-literal move diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingAffineHandleAfterEnumConstructorMove(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

enum Job:
    Run(thread: Thread[i64, Joinable])

def bad(thread: Thread[i64, Joinable]) -> i64:
    job: Job = Job.Run(thread: move thread)
    return join(move thread)
`
	_, errs := parseAndAnalyze(t, "affine_enum_constructor_move_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"thread\" cannot be used: usage facts were consumed by move into enum payload \"Job.Run.thread\"") {
		t.Fatalf("expected enum-constructor move diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsUsingAffineFieldAfterParentMove(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: Holder) -> i64:
    copy: Holder = move holder
    return join(move holder.thread)
`
	_, errs := parseAndAnalyze(t, "affine_parent_move_field_use_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"holder.thread\" cannot be used: usage facts were consumed by move into local \"copy\"") {
		t.Fatalf("expected parent-move child-use diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingIndexedAffineValue(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def bad(items: array[Thread[i64, Joinable], 1]) -> i64:
    first: i64 = join(move items[0])
    _ = first
    return join(move items[0])
`
	_, errs := parseAndAnalyze(t, "affine_index_move_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "value containing linear handles \"items\" cannot be used: usage facts were consumed by argument to call \"join\"") {
		t.Fatalf("expected indexed-affine diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeTypeMismatchAssignment(t *testing.T) {
	src := `def mismatch() -> int:
    value: mutable int = true
    return value
`
	_, errs := parseAndAnalyze(t, "type_mismatch.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects int, got bool") {
		t.Fatalf("expected assignment mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsFloatArithmeticAndCastShorthand(t *testing.T) {
	src := `def combine(left: f32, right: f64) -> f64:
    total: f64 = left.f64() + right
    return total / 2.0

def narrow(value: f64) -> f32:
    return value.f32()
`
	result, errs := parseAndAnalyze(t, "float_arithmetic_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "combine", "f64")
	requireFunctionReturnTypeString(t, result, "narrow", "f32")
}
func TestAnalyzeRejectsFloatModulo(t *testing.T) {
	src := `def bad(value: f64) -> f64:
    return value % 2.0
`
	_, errs := parseAndAnalyze(t, "float_modulo_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "operator requires integral operands") {
		t.Fatalf("expected integral-operand diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsFloatArrayIndex(t *testing.T) {
	src := `def bad(values: i32[4], idx: f64) -> i32:
    return values[idx]
`
	_, errs := parseAndAnalyze(t, "float_index_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "index must be integral, got f64") {
		t.Fatalf("expected integral-index diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsConstFloatCastsInCompileTimeIntegerContexts(t *testing.T) {
	src := `const COUNT: i32 = 3.75.i32()

def sized() -> i32[COUNT]:
    return [1, 2, 3]
`
	result, errs := parseAndAnalyze(t, "const_float_cast_compile_time_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	count, ok := result.ConstValues["COUNT"]
	if !ok {
		t.Fatal("expected COUNT const value to be recorded")
	}
	if count.Kind != semantic.ConstInt || count.Int != 3 {
		t.Fatalf("expected COUNT const value to be int 3, got %#v", count)
	}
}
func TestAnalyzeAcceptsExtendedFloatCastMatrix(t *testing.T) {
	src := `def f64_to_i64(value: f64) -> i64:
	return value.i64()

def f64_to_u32(value: f64) -> u32:
	return value.u32()

def f64_to_u64(value: f64) -> u64:
	return value.u64()

def f32_to_f64(value: f32) -> f64:
	return value.f64()

def i64_to_f64(value: i64) -> f64:
	return value.f64()

def u32_to_f32(value: u32) -> f32:
	return value.f32()

def u64_to_f64(value: u64) -> f64:
	return value.f64()

def usize_to_f64(value: usize) -> f64:
	return value.f64()
`
	result, errs := parseAndAnalyze(t, "float_cast_matrix_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "f64_to_i64", "i64")
	requireFunctionReturnTypeString(t, result, "f64_to_u32", "u32")
	requireFunctionReturnTypeString(t, result, "f64_to_u64", "u64")
	requireFunctionReturnTypeString(t, result, "f32_to_f64", "f64")
	requireFunctionReturnTypeString(t, result, "i64_to_f64", "f64")
	requireFunctionReturnTypeString(t, result, "u32_to_f32", "f32")
	requireFunctionReturnTypeString(t, result, "u64_to_f64", "f64")
	requireFunctionReturnTypeString(t, result, "usize_to_f64", "f64")
}
func TestAnalyzeAcceptsConstExtendedFloatCastMatrix(t *testing.T) {
	src := `const I64_FROM_F64: i64 = 8.75.i64()
const U32_FROM_F64: u32 = 5.5.u32()
const U64_FROM_F64: u64 = 6.5.u64()
const F64_FROM_U32: f64 = 7.i32().u32().f64()
const F32_FROM_U64: f32 = 9.i32().u64().f32()

def total() -> f64:
	return I64_FROM_F64.f64() + U32_FROM_F64.f64() + U64_FROM_F64.f64() + F64_FROM_U32 + F32_FROM_U64.f64()
`
	result, errs := parseAndAnalyze(t, "const_float_cast_matrix_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	checks := map[string]semantic.ConstValue{
		"I64_FROM_F64": {Kind: semantic.ConstInt, Int: 8},
		"U32_FROM_F64": {Kind: semantic.ConstInt, Int: 5},
		"U64_FROM_F64": {Kind: semantic.ConstInt, Int: 6},
		"F64_FROM_U32": {Kind: semantic.ConstFloat, Float: 7},
		"F32_FROM_U64": {Kind: semantic.ConstFloat, Float: 9},
	}
	for name, want := range checks {
		got, ok := result.ConstValues[name]
		if !ok {
			t.Fatalf("expected %s const value to be recorded", name)
		}
		if got.Kind != want.Kind {
			t.Fatalf("expected %s const kind %v, got %#v", name, want.Kind, got)
		}
		switch want.Kind {
		case semantic.ConstInt:
			if got.Int != want.Int {
				t.Fatalf("expected %s const int %d, got %#v", name, want.Int, got)
			}
		case semantic.ConstFloat:
			if got.Float != want.Float {
				t.Fatalf("expected %s const float %v, got %#v", name, want.Float, got)
			}
		default:
			t.Fatalf("unexpected expected const kind for %s: %#v", name, want)
		}
	}
}
func TestAnalyzeRejectsConstFloatCastEdgeCases(t *testing.T) {
	src := `const NEG_TO_U32: u32 = (-1.0).u32()
const BIG_TO_I8: i8 = 200.0.i8()
const BIG_TO_I64: i64 = 9223372036854775808.0.i64()
const BIG_TO_U64: u64 = 9223372036854775808.0.u64()
`
	_, errs := parseAndAnalyze(t, "const_float_cast_edge_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "const \"NEG_TO_U32\" initializer must be a compile-time u32 value") {
		t.Fatalf("expected unsigned const-cast rejection, got:\n%s", all)
	}
	if !strings.Contains(all, "const \"BIG_TO_I8\" initializer must be a compile-time i8 value") {
		t.Fatalf("expected narrowing const-cast rejection, got:\n%s", all)
	}
	if !strings.Contains(all, "const \"BIG_TO_I64\" initializer must be a compile-time i64 value") {
		t.Fatalf("expected signed overflow const-cast rejection, got:\n%s", all)
	}
	if !strings.Contains(all, "const \"BIG_TO_U64\" initializer must be a compile-time u64 value") {
		t.Fatalf("expected unsigned overflow const-cast rejection, got:\n%s", all)
	}
}
func TestAnalyzeContextualFloatLiteralSites(t *testing.T) {
	src := `extern passthrough(value: f32) -> f32

struct FloatPair:
	left: f32
	right: f32

def contextual_local() -> f32:
	local: f32 = 1.25
	return local

def contextual_return(flag: bool) -> f32:
	return (2.5 if flag else 3.5)

def contextual_call() -> f32:
	return passthrough(4.5)

def contextual_struct() -> FloatPair:
	return FloatPair{left: 5.5, right: 6.5}

def contextual_array() -> f32[2]:
	return [7.5, 8.5]
`
	result, errs := parseAndAnalyze(t, "contextual_float_literals_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	localDecl := requireFuncDecl(t, result, "contextual_local")
	localInit, ok := contextualFuncStmt(localDecl, 0).(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected contextual_local to start with a local declaration, got %T", contextualFuncStmt(localDecl, 0))
	}
	requireExprTypeString(t, result, localInit.Value, "f32")

	returnDecl := requireFuncDecl(t, result, "contextual_return")
	returnStmt, ok := contextualFuncStmt(returnDecl, 0).(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_return to contain a return statement, got %T", contextualFuncStmt(returnDecl, 0))
	}
	parenExpr, ok := returnStmt.Value.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected contextual_return to return a parenthesized ternary, got %T", returnStmt.Value)
	}
	ternaryExpr, ok := parenExpr.Inner.(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("expected contextual_return to return a ternary, got %T", parenExpr.Inner)
	}
	requireExprTypeString(t, result, ternaryExpr, "f32")
	requireExprTypeString(t, result, ternaryExpr.Value, "f32")
	requireExprTypeString(t, result, ternaryExpr.Alt, "f32")

	callDecl := requireFuncDecl(t, result, "contextual_call")
	callReturn, ok := contextualFuncStmt(callDecl, 0).(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_call to contain a return statement, got %T", contextualFuncStmt(callDecl, 0))
	}
	callExpr, ok := callReturn.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected contextual_call to return a function call, got %T", callReturn.Value)
	}
	requireExprTypeString(t, result, callExpr.Args[0], "f32")

	structDecl := requireFuncDecl(t, result, "contextual_struct")
	structReturn, ok := contextualFuncStmt(structDecl, 0).(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_struct to contain a return statement, got %T", contextualFuncStmt(structDecl, 0))
	}
	structLit, ok := structReturn.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected contextual_struct to return a struct literal, got %T", structReturn.Value)
	}
	for _, arg := range structLit.Args {
		requireExprTypeString(t, result, arg, "f32")
	}

	arrayDecl := requireFuncDecl(t, result, "contextual_array")
	arrayReturn, ok := contextualFuncStmt(arrayDecl, 0).(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_array to contain a return statement, got %T", contextualFuncStmt(arrayDecl, 0))
	}
	arrayLit, ok := arrayReturn.Value.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected contextual_array to return an array literal, got %T", arrayReturn.Value)
	}
	for _, elem := range arrayLit.Elems {
		requireExprTypeString(t, result, elem, "f32")
	}
}
