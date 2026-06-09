package semantic_test

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestAnalyzeContextualFloatLiteralArithmeticSites(t *testing.T) {
	src := `extern passthrough(value: f32) -> f32

struct FloatPair:
	left: f32
	right: f32

def contextual_local() -> f32:
	local: f32 = 1.25 + 2.25
	return local

def contextual_return(flag: bool) -> f32:
	return ((3.25 + 4.25) if flag else (5.25 + 6.25))

def contextual_call() -> f32:
	return passthrough(7.25 + 8.25)

def contextual_struct() -> FloatPair:
	return FloatPair(9.25 + 10.25, 11.25 + 12.25)

def contextual_array() -> f32[2]:
	return [13.25 + 14.25, 15.25 + 16.25]
`
	result, errs := parseAndAnalyze(t, "contextual_float_literal_arithmetic_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	localDecl := requireFuncDecl(t, result, "contextual_local")
	localInit, ok := localDecl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected contextual_local to start with a local declaration, got %T", localDecl.Body[0])
	}
	localBinary, ok := localInit.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected contextual_local initializer to be a binary expression, got %T", localInit.Value)
	}
	requireExprTypeString(t, result, localBinary, "f32")
	requireExprTypeString(t, result, localBinary.Left, "f32")
	requireExprTypeString(t, result, localBinary.Right, "f32")

	returnDecl := requireFuncDecl(t, result, "contextual_return")
	returnStmt, ok := returnDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_return to contain a return statement, got %T", returnDecl.Body[0])
	}
	parenExpr, ok := returnStmt.Value.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected contextual_return to return a parenthesized ternary, got %T", returnStmt.Value)
	}
	ternaryExpr, ok := parenExpr.Inner.(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("expected contextual_return to return a ternary, got %T", parenExpr.Inner)
	}
	thenParen, ok := ternaryExpr.Value.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected contextual_return true branch to be parenthesized, got %T", ternaryExpr.Value)
	}
	thenBinary, ok := thenParen.Inner.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected contextual_return true branch to be a binary expression, got %T", thenParen.Inner)
	}
	elseParen, ok := ternaryExpr.Alt.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected contextual_return false branch to be parenthesized, got %T", ternaryExpr.Alt)
	}
	elseBinary, ok := elseParen.Inner.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected contextual_return false branch to be a binary expression, got %T", elseParen.Inner)
	}
	requireExprTypeString(t, result, ternaryExpr, "f32")
	for _, expr := range []ast.Expr{thenBinary, thenBinary.Left, thenBinary.Right, elseBinary, elseBinary.Left, elseBinary.Right} {
		requireExprTypeString(t, result, expr, "f32")
	}

	callDecl := requireFuncDecl(t, result, "contextual_call")
	callReturn, ok := callDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_call to contain a return statement, got %T", callDecl.Body[0])
	}
	callExpr, ok := callReturn.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected contextual_call to return a function call, got %T", callReturn.Value)
	}
	callBinary, ok := callExpr.Args[0].(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected contextual_call argument to be a binary expression, got %T", callExpr.Args[0])
	}
	requireExprTypeString(t, result, callBinary, "f32")
	requireExprTypeString(t, result, callBinary.Left, "f32")
	requireExprTypeString(t, result, callBinary.Right, "f32")

	structDecl := requireFuncDecl(t, result, "contextual_struct")
	structReturn, ok := structDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_struct to contain a return statement, got %T", structDecl.Body[0])
	}
	structLit, ok := structReturn.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected contextual_struct to return a struct literal, got %T", structReturn.Value)
	}
	for _, arg := range structLit.Args {
		binary, ok := arg.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected contextual_struct field to be a binary expression, got %T", arg)
		}
		requireExprTypeString(t, result, binary, "f32")
		requireExprTypeString(t, result, binary.Left, "f32")
		requireExprTypeString(t, result, binary.Right, "f32")
	}

	arrayDecl := requireFuncDecl(t, result, "contextual_array")
	arrayReturn, ok := arrayDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_array to contain a return statement, got %T", arrayDecl.Body[0])
	}
	arrayLit, ok := arrayReturn.Value.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected contextual_array to return an array literal, got %T", arrayReturn.Value)
	}
	for _, elem := range arrayLit.Elems {
		binary, ok := elem.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected contextual_array element to be a binary expression, got %T", elem)
		}
		requireExprTypeString(t, result, binary, "f32")
		requireExprTypeString(t, result, binary.Left, "f32")
		requireExprTypeString(t, result, binary.Right, "f32")
	}
}
func TestAnalyzeContextualFloatLiteralArithmeticTopLevelSites(t *testing.T) {
	src := `struct FloatPair:
	left: f32
	right: f32

const F32_TOTAL: f32 = 1.25 + 2.25
const F64_TOTAL: f64 = 3.25 + 4.25

global g_f32: f32 = 5.25 + 6.25
global g_f64: f64 = 7.25 + 8.25
global g_pair: FloatPair = FloatPair(9.25 + 10.25, 11.25 + 12.25)
global g_values: f32[2] = [13.25 + 14.25, 15.25 + 16.25]
`
	result, errs := parseAndAnalyze(t, "contextual_float_literal_arithmetic_toplevel_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	checks := map[string]semantic.ConstValue{
		"F32_TOTAL": {Kind: semantic.ConstFloat, Float: 3.5},
		"F64_TOTAL": {Kind: semantic.ConstFloat, Float: 7.5},
	}
	for name, want := range checks {
		got, ok := result.ConstValues[name]
		if !ok {
			t.Fatalf("expected %s const value to be recorded", name)
		}
		if got.Kind != want.Kind || got.Float != want.Float {
			t.Fatalf("expected %s const value %#v, got %#v", name, want, got)
		}
	}

	constF32 := requireConstDecl(t, result, "F32_TOTAL")
	constF32Binary, ok := constF32.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected F32_TOTAL initializer to be a binary expression, got %T", constF32.Value)
	}
	requireExprTypeString(t, result, constF32Binary, "f32")
	requireExprTypeString(t, result, constF32Binary.Left, "f32")
	requireExprTypeString(t, result, constF32Binary.Right, "f32")

	constF64 := requireConstDecl(t, result, "F64_TOTAL")
	constF64Binary, ok := constF64.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected F64_TOTAL initializer to be a binary expression, got %T", constF64.Value)
	}
	requireExprTypeString(t, result, constF64Binary, "f64")
	requireExprTypeString(t, result, constF64Binary.Left, "f64")
	requireExprTypeString(t, result, constF64Binary.Right, "f64")

	globalF32 := requireGlobalDecl(t, result, "g_f32")
	globalF32Binary, ok := globalF32.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected g_f32 initializer to be a binary expression, got %T", globalF32.Value)
	}
	requireExprTypeString(t, result, globalF32Binary, "f32")
	requireExprTypeString(t, result, globalF32Binary.Left, "f32")
	requireExprTypeString(t, result, globalF32Binary.Right, "f32")

	globalF64 := requireGlobalDecl(t, result, "g_f64")
	globalF64Binary, ok := globalF64.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected g_f64 initializer to be a binary expression, got %T", globalF64.Value)
	}
	requireExprTypeString(t, result, globalF64Binary, "f64")
	requireExprTypeString(t, result, globalF64Binary.Left, "f64")
	requireExprTypeString(t, result, globalF64Binary.Right, "f64")

	globalPair := requireGlobalDecl(t, result, "g_pair")
	pairLit, ok := globalPair.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected g_pair initializer to be a struct literal, got %T", globalPair.Value)
	}
	for _, arg := range pairLit.Args {
		binary, ok := arg.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected g_pair field to be a binary expression, got %T", arg)
		}
		requireExprTypeString(t, result, binary, "f32")
		requireExprTypeString(t, result, binary.Left, "f32")
		requireExprTypeString(t, result, binary.Right, "f32")
	}

	globalValues := requireGlobalDecl(t, result, "g_values")
	listLit, ok := globalValues.Value.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected g_values initializer to be a list literal, got %T", globalValues.Value)
	}
	for _, elem := range listLit.Elems {
		binary, ok := elem.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected g_values element to be a binary expression, got %T", elem)
		}
		requireExprTypeString(t, result, binary, "f32")
		requireExprTypeString(t, result, binary.Left, "f32")
		requireExprTypeString(t, result, binary.Right, "f32")
	}
}
func TestAnalyzeContextualIntLiteralUsizeSites(t *testing.T) {
	src := `def contextual_local() -> usize:
	which: usize = 1
	return which

def contextual_index(items: i32[4]) -> i32:
	return items[0]

def contextual_slice(items: view[i32]) -> view[i32]:
	window: view[i32] = items[1:3]
	return window

def plain_range() -> int:
	total: mutable int = 0
	for i in 0..<4:
		idx: int = i
		total <- total + idx
	return total
`
	result, errs := parseAndAnalyze(t, "contextual_int_literals_usize_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	localDecl := requireFuncDecl(t, result, "contextual_local")
	localInit, ok := localDecl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected contextual_local to start with a local declaration, got %T", localDecl.Body[0])
	}
	requireExprTypeString(t, result, localInit.Value, "usize")

	indexDecl := requireFuncDecl(t, result, "contextual_index")
	indexReturn, ok := indexDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_index to contain a return statement, got %T", indexDecl.Body[0])
	}
	indexExpr, ok := indexReturn.Value.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected contextual_index to return an index expression, got %T", indexReturn.Value)
	}
	requireExprTypeString(t, result, indexExpr.Index, "usize")

	sliceDecl := requireFuncDecl(t, result, "contextual_slice")
	sliceInit, ok := sliceDecl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected contextual_slice to start with a local declaration, got %T", sliceDecl.Body[0])
	}
	sliceExpr, ok := sliceInit.Value.(*ast.SliceExpr)
	if !ok {
		t.Fatalf("expected contextual_slice initializer to be a slice expression, got %T", sliceInit.Value)
	}
	requireExprTypeString(t, result, sliceExpr.Start, "usize")
	requireExprTypeString(t, result, sliceExpr.End, "usize")

	rangeDecl := requireFuncDecl(t, result, "plain_range")
	forStmt, ok := rangeDecl.Body[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected plain_range second statement to be a for loop, got %T", rangeDecl.Body[1])
	}
	requireExprTypeString(t, result, forStmt.Start, "int")
	requireExprTypeString(t, result, forStmt.End, "int")
	idxDecl, ok := forStmt.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected plain_range loop body to start with a local declaration, got %T", forStmt.Body[0])
	}
	requireExprTypeString(t, result, idxDecl.Value, "int")
}
func TestAnalyzeContextualIntLiteralI32Sites(t *testing.T) {
	src := `extern passthrough(value: i32) -> i32

struct Pair:
	left: i32
	right: i32

def contextual_local() -> i32:
	value: i32 = 1
	return value

def contextual_call() -> i32:
	return passthrough(2)

def contextual_struct() -> Pair:
	return Pair(3, 4)

def contextual_array() -> i32[2]:
	return [5, 6]

def contextual_unary() -> i32:
	return -7
`
	result, errs := parseAndAnalyze(t, "contextual_int_literals_i32_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	localDecl := requireFuncDecl(t, result, "contextual_local")
	localInit, ok := localDecl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected contextual_local to start with a local declaration, got %T", localDecl.Body[0])
	}
	requireExprTypeString(t, result, localInit.Value, "i32")

	callDecl := requireFuncDecl(t, result, "contextual_call")
	callReturn, ok := callDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_call to contain a return statement, got %T", callDecl.Body[0])
	}
	callExpr, ok := callReturn.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected contextual_call to return a function call, got %T", callReturn.Value)
	}
	requireExprTypeString(t, result, callExpr.Args[0], "i32")

	structDecl := requireFuncDecl(t, result, "contextual_struct")
	structReturn, ok := structDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_struct to contain a return statement, got %T", structDecl.Body[0])
	}
	structLit, ok := structReturn.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected contextual_struct to return a struct literal, got %T", structReturn.Value)
	}
	for _, arg := range structLit.Args {
		requireExprTypeString(t, result, arg, "i32")
	}

	arrayDecl := requireFuncDecl(t, result, "contextual_array")
	arrayReturn, ok := arrayDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_array to contain a return statement, got %T", arrayDecl.Body[0])
	}
	arrayLit, ok := arrayReturn.Value.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected contextual_array to return an array literal, got %T", arrayReturn.Value)
	}
	for _, elem := range arrayLit.Elems {
		requireExprTypeString(t, result, elem, "i32")
	}

	unaryDecl := requireFuncDecl(t, result, "contextual_unary")
	unaryReturn, ok := unaryDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected contextual_unary to contain a return statement, got %T", unaryDecl.Body[0])
	}
	unaryExpr, ok := unaryReturn.Value.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected contextual_unary to return a unary expression, got %T", unaryReturn.Value)
	}
	requireExprTypeString(t, result, unaryExpr, "i32")
	requireExprTypeString(t, result, unaryExpr.Operand, "i32")
}
func TestAnalyzeExplicitIntLiteralSuffixOverridesUsizeContext(t *testing.T) {
	src := `def ok() -> usize:
	which: usize = 1i32
	return which
`
	result, errs := parseAndAnalyze(t, "contextual_int_literal_suffix_override_ok.elisa", src)
	requireNoErrors(t, errs)
	warns := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(warns, `numeric literal suffix "i32" is discouraged`) {
		t.Fatalf("expected numeric suffix warning, got:\n%s", warns)
	}

	fn := requireFuncDecl(t, result, "ok")
	localInit, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected ok to start with a local declaration, got %T", fn.Body[0])
	}
	requireExprTypeString(t, result, localInit.Value, "i32")
}
func TestAnalyzeRejectsNullIntoNonNullRef(t *testing.T) {
	src := `struct Box:
    value: int

def bad() -> void:
    box: Box& = null
`
	_, errs := parseAndAnalyze(t, "nonnull_ref_rejects_null.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects Box&, got null") {
		t.Fatalf("expected non-null ref rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzePointerTypestateBranches(t *testing.T) {
	src := `struct Box:
    value: mutable int

extern alloc_box() -> Box&?
extern sfree_box(box: Box&) -> Box!

def release_box() -> void:
	box: mutable Box&? = alloc_box()
    if box != null:
        box as ! <- sfree_box(box)

def missing_box() -> Box!:
    return null
`
	_, errs := parseAndAnalyze(t, "pointer_typestate.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsNullableFieldAccessWithoutProof(t *testing.T) {
	src := `struct Box:
    value: mutable int

extern maybe_box() -> Box&?

def bad() -> int:
	box: Box&? = maybe_box()
    return box.value
`
	_, errs := parseAndAnalyze(t, "nullable_field_access.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "field access requires proven non-null reference") {
		t.Fatalf("expected nullable field access rejection, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeGuardClauseRefinesAfterReturn(t *testing.T) {
	src := `struct Box:
    value: mutable int

extern maybe_box() -> Box&?

def read_box() -> int:
	box: Box&? = maybe_box()
    if box == null:
        return 0
    return box.value
`
	_, errs := parseAndAnalyze(t, "guard_clause_refinement.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAliasGuardClauseRefinesRootAfterReturn(t *testing.T) {
	src := `struct Box:
	value: mutable int

extern maybe_box() -> Box&?

def read_box() -> int:
	box: Box&? = maybe_box()
	alias: Box&? = box
	if alias == null:
		return 0
	return box.value
`
	_, errs := parseAndAnalyze(t, "alias_guard_clause_refines_root_after_return.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAliasRefinementInvalidatesAfterRootAssignment(t *testing.T) {
	src := `struct Box:
	value: mutable int

extern maybe_box() -> Box&?

def bad_box() -> int:
	box: mutable Box&? = maybe_box()
	alias: Box&? = box
	if alias == null:
		return 0
	box <- null
	return alias.value
`
	_, errs := parseAndAnalyze(t, "alias_refinement_invalidates_after_root_assignment.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "field access requires proven non-null reference") {
		t.Fatalf("expected alias refinement invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsBuiltinSurfaceCollectionTypes(t *testing.T) {
	src := `extern take_array(values: array[i32, 4]) -> void
extern take_darray(values: darray[i32, row]) -> void
extern take_cstr(text: cstr[row]) -> void
extern take_view(values: view[i32, 0, 2]) -> void
extern take_sview(text: sview[1, 4]) -> void

def use(values: array[i32, 4], dyn: darray[i32, row], text: str[5], dyn_text: cstr[row]) -> char:
	sub_array: view[i32, 0, 2] = values[0:2]
	sub_text: sview[1, 4] = text[1:4]
	dyn_sub: sview[0, 1] = dyn_text[0:1]
	take_array(values)
	take_darray(dyn)
	take_cstr(dyn_text)
	take_view(sub_array)
	take_sview(sub_text)
	take_sview(dyn_sub)
	return text[0]
`
	_, errs := parseAndAnalyze(t, "builtin_surface_types.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsCharCastAndComparisonFromStringIndexing(t *testing.T) {
	src := `def first_code(text: str[4]) -> i64:
	ch: char = text[0]
	return ch.i64()

def same_head(left: cstr[row], right: cstr[col]) -> bool:
	return left[0] == right[0]
`
	_, errs := parseAndAnalyze(t, "char_cast_and_compare.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsStandaloneCharLocalsParamsAndCasts(t *testing.T) {
	src := `def normalize(code: i64) -> char:
	ch: char = code.char()
	if ch == 0.char():
		return 65.char()
	return ch

def bump(ch: char) -> i64:
	return (ch + 1).i64()
`
	_, errs := parseAndAnalyze(t, "standalone_char_values.elisa", src)
	requireNoErrors(t, errs)
}
