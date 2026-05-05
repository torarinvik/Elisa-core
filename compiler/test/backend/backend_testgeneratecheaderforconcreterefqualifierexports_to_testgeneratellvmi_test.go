package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateCHeaderForConcreteRefQualifierExports(t *testing.T) {
	src := `struct Node:
	value: mutable i32

struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

export type Node as CtxNode
export type Handle[heap, &] as HeapHandle

def keep_handle[refstorage Store, refstate State](value: Handle[Store, State]) -> Handle[Store, State]:
	return value

export func keep_heap_handle(value: HeapHandle) -> HeapHandle = keep_handle[heap, &]
`
	result := parseAndAnalyze(t, "backend_ref_qualifier_export_header.elisa", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	checks := []string{
		"typedef struct CtxNode CtxNode;",
		"typedef struct HeapHandle HeapHandle;",
		"struct CtxNode {",
		"struct HeapHandle {",
		"CtxNode *ptr;",
		"HeapHandle keep_heap_handle(HeapHandle arg0);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
	if strings.Contains(header, "Handle__heap__anon") {
		t.Fatalf("expected public header not to leak qualifier-specialized backend names, got:\n%s", header)
	}
}
func TestGenerateLLVMIRLowersFloatArithmeticComparisonsAndCasts(t *testing.T) {
	src := `global tau: f64 = 6.25

def mix(left: f32, right: f64) -> f64:
	total: f64 = left.f64() + right
	return total * tau

def negate(value: f64) -> f64:
	return -value

def same(left: f64, right: f64) -> bool:
	return left == right

def widen_bits(value: i32) -> f64:
	return value.f64()

def narrow(value: f64) -> f32:
	return value.f32()
`
	result := parseAndAnalyze(t, "backend_float_ops.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"@tau = global double",
		"define double @mix(float",
		"fpext float",
		"fadd double",
		"fmul double",
		"define double @negate(double",
		"fneg double",
		"define i1 @same(double",
		"fcmp oeq double",
		"define double @widen_bits(i32",
		"sitofp i32",
		"define float @narrow(double",
		"fptrunc double",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateCHeaderUsesFloatBuiltinMappings(t *testing.T) {
	src := `struct Metrics:
	ratio: f32
	total: f64

export type Metrics as MetricsFFI

global tau: f64 = 6.25
export global tau as ctx_tau

def scale_sum_impl(left: f32, right: f64) -> f64:
	return left.f64() + right

export func scale_sum(left: f32, right: f64) -> f64 = scale_sum_impl
`
	result := parseAndAnalyze(t, "backend_float_header.elisa", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	checks := []string{
		"typedef struct MetricsFFI MetricsFFI;",
		"struct MetricsFFI {",
		"float ratio;",
		"double total;",
		"extern double ctx_tau;",
		"double scale_sum(float arg0, double arg1);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
}
func TestGenerateLLVMIRLowersConstFloatCastsForGlobals(t *testing.T) {
	src := `const SMALL: i32 = 3.75.i32()
const RATIO32: f32 = 1.5.f32()
const WIDE64: f64 = 7.i32().f64()

global g_small: i32 = SMALL
global g_ratio32: f32 = RATIO32
global g_wide64: f64 = WIDE64
`
	result := parseAndAnalyze(t, "backend_const_float_cast_globals.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"@g_small = global i32 3",
		"@g_ratio32 = global float",
		"@g_wide64 = global double 7.000000e+00",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{
		"@g_small = global i32 3.750000e+00",
		"@g_wide64 = global double 3.750000e+00",
	} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected output to avoid %q, got:\n%s", bad, output)
		}
	}
}
func TestGenerateLLVMIRLowersExtendedFloatCastMatrix(t *testing.T) {
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
`
	result := parseAndAnalyze(t, "backend_float_cast_matrix.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @f64_to_i64(double",
		"fptosi double",
		"define i32 @f64_to_u32(double",
		"fptoui double",
		"define i64 @f64_to_u64(double",
		"define double @f32_to_f64(float",
		"fpext float",
		"define double @i64_to_f64(i64",
		"sitofp i64",
		"define float @u32_to_f32(i32",
		"uitofp i32",
		"define double @u64_to_f64(i64",
		"uitofp i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if got := strings.Count(output, "fptoui double"); got < 2 {
		t.Fatalf("expected at least two unsigned float-to-int casts, got %d:\n%s", got, output)
	}
}
func TestGenerateLLVMIRLowersExtendedConstFloatCastMatrixForGlobals(t *testing.T) {
	src := `const I64_FROM_F64: i64 = 8.75.i64()
const U32_FROM_F64: u32 = 5.5.u32()
const U64_FROM_F64: u64 = 6.5.u64()
const F64_FROM_U32: f64 = 7.i32().u32().f64()
const F32_FROM_U64: f32 = 9.i32().u64().f32()

global g_i64: i64 = I64_FROM_F64
global g_u32: u32 = U32_FROM_F64
global g_u64: u64 = U64_FROM_F64
global g_f64: f64 = F64_FROM_U32
global g_f32: f32 = F32_FROM_U64
`
	result := parseAndAnalyze(t, "backend_const_float_cast_matrix_globals.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"@g_i64 = global i64 8",
		"@g_u32 = global i32 5",
		"@g_u64 = global i64 6",
		"@g_f64 = global double 7.000000e+00",
		"@g_f32 = global float 9.000000e+00",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{
		"@g_u32 = global i32 5.500000e+00",
		"@g_u64 = global i64 6.500000e+00",
	} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected output to avoid %q, got:\n%s", bad, output)
		}
	}
}
func TestGenerateLLVMIRLowersContextualFloatLiteralSitesAndGlobals(t *testing.T) {
	src := `extern passthrough(value: f32) -> f32

struct FloatPair:
	left: f32
	right: f32

global g_ratio: f32 = 1.25
global g_pair: FloatPair = FloatPair(2.5, 3.5)
global g_values: f32[2] = [4.5, 5.5]

def choose(flag: bool) -> f32:
	return (6.5 if flag else 7.5)

def local_and_call() -> f32:
	local: f32 = 8.5
	return passthrough(local) + passthrough(9.5)
`
	result := parseAndAnalyze(t, "backend_contextual_float_literals.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%FloatPair = type { float, float }",
		"@g_ratio = global float",
		"@g_pair = global %FloatPair",
		"@g_values = global [2 x float]",
		"declare float @passthrough(float)",
		"define float @choose(i1",
		"define float @local_and_call()",
		"call float @passthrough(float",
		"fadd float",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	chooseBody := functionIR(output, "choose")
	if chooseBody == "" {
		t.Fatalf("expected to find choose body, got:\n%s", output)
	}
	if strings.Contains(chooseBody, "fptrunc double") {
		t.Fatalf("expected choose to avoid redundant double-to-float truncation, got:\n%s", chooseBody)
	}

	localBody := functionIR(output, "local_and_call")
	if localBody == "" {
		t.Fatalf("expected to find local_and_call body, got:\n%s", output)
	}
	if strings.Contains(localBody, "fptrunc double") {
		t.Fatalf("expected local_and_call to avoid redundant double-to-float truncation, got:\n%s", localBody)
	}
}
func TestGenerateLLVMIRLowersContextualFloatLiteralArithmeticSites(t *testing.T) {
	src := `extern passthrough(value: f32) -> f32

def choose(flag: bool) -> f32:
	return ((1.25 + 2.25) if flag else (3.25 + 4.25))

def local_and_call() -> f32:
	local: f32 = 5.25 + 6.25
	return passthrough(local) + passthrough(7.25 + 8.25)
`
	result := parseAndAnalyze(t, "backend_contextual_float_literal_arithmetic.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare float @passthrough(float)",
		"define float @choose(i1",
		"define float @local_and_call()",
		"fadd float",
		"call float @passthrough(float",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	for _, name := range []string{"choose", "local_and_call"} {
		body := functionIR(output, name)
		if body == "" {
			t.Fatalf("expected to find %s body, got:\n%s", name, output)
		}
		if strings.Contains(body, "fptrunc double") {
			t.Fatalf("expected %s to avoid redundant double-to-float truncation, got:\n%s", name, body)
		}
		if strings.Contains(body, "fadd double") {
			t.Fatalf("expected %s to stay in float arithmetic, got:\n%s", name, body)
		}
	}
}
func TestGenerateLLVMIRLowersContextualFloatLiteralArithmeticTopLevelSites(t *testing.T) {
	src := `struct FloatPair:
	left: f32
	right: f32

const F32_TOTAL: f32 = 1.25 + 2.25
const F64_TOTAL: f64 = 3.25 + 4.25

global g_f32: f32 = 5.25 + 6.25
global g_f64: f64 = 7.25 + 8.25
global g_pair: FloatPair = FloatPair(9.25 + 10.25, 11.25 + 12.25)
global g_values: f32[2] = [13.25 + 14.25, 15.25 + 16.25]

def total() -> f64:
	return F32_TOTAL.f64() + F64_TOTAL + g_f32.f64() + g_f64
`
	result := parseAndAnalyze(t, "backend_contextual_float_literal_arithmetic_toplevel.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%FloatPair = type { float, float }",
		"@g_f32 = global float 1.150000e+01",
		"@g_f64 = global double 1.550000e+01",
		"@g_pair = global %FloatPair { float 1.950000e+01, float 2.350000e+01 }",
		"@g_values = global [2 x float] [float 2.750000e+01, float 3.150000e+01]",
		"define double @total()",
		"fadd double",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	totalBody := functionIR(output, "total")
	if totalBody == "" {
		t.Fatalf("expected to find total body, got:\n%s", output)
	}
	if strings.Contains(totalBody, "fptrunc double") {
		t.Fatalf("expected total to avoid redundant double-to-float truncation, got:\n%s", totalBody)
	}
}
func TestGenerateCHeaderOrdersAggregateDefinitionsByValueDependencies(t *testing.T) {
	src := `struct Node:
	value: mutable i32
	next: mutable Node&?

struct Wrapper:
	node: mutable Node
	next_ref: mutable Node&?

export type Wrapper as CtxWrapper
export type Node as CtxNode

global root: Wrapper = zeroed
export global root as ctx_root
`
	result := parseAndAnalyze(t, "backend_export_header_order.elisa", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	nodeIndex := strings.Index(header, "struct CtxNode {")
	wrapperIndex := strings.Index(header, "struct CtxWrapper {")
	if nodeIndex == -1 || wrapperIndex == -1 {
		t.Fatalf("expected both exported structs in header, got:\n%s", header)
	}
	if nodeIndex > wrapperIndex {
		t.Fatalf("expected Node definition before Wrapper definition, got:\n%s", header)
	}
	if !strings.Contains(header, "CtxNode *next;") {
		t.Fatalf("expected pointer field to use forward-declared public name, got:\n%s", header)
	}
	if !strings.Contains(header, "extern CtxWrapper ctx_root;") {
		t.Fatalf("expected exported global declaration, got:\n%s", header)
	}
}
func TestGenerateLLVMIRLowersVariadicExternCalls(t *testing.T) {
	src := `extern snprintf(buffer: u8&?, buffer_size: usize, format: u8&, ...) -> int

def format_len(format: u8&) -> int:
	return snprintf(null, 0, format, 7, 9)
`
	result := parseAndAnalyze(t, "backend_variadic_call.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @snprintf(ptr, i64, ptr, ...)",
		"define i64 @format_len(ptr",
		"call i64 (ptr, i64, ptr, ...) @snprintf(",
		"ptr null, i64 0",
		"i64 7, i64 9)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersPointerIntegerCasts(t *testing.T) {
	src := `def ptr_bits(ptr: u8&) -> uintptr:
	return ptr.uintptr()

def bits_ptr(bits: uintptr) -> u8&:
	return bits.cast[u8&]
`
	result := parseAndAnalyze(t, "backend_pointer_integer_casts.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @ptr_bits(ptr",
		"ptrtoint ptr",
		"define ptr @bits_ptr(i64",
		"inttoptr i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersNestedFieldAccessOnReturnedStructValues(t *testing.T) {
	src := `struct Inner:
	value: i32

struct Outer:
	inner: Inner

extern make_outer() -> Outer

def read_nested_return() -> i32:
	return make_outer().inner.value
`
	result := parseAndAnalyze(t, "backend_nested_return_field_chain.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Inner = type { i32 }",
		"%Outer = type { %Inner }",
		"declare %Outer @make_outer()",
		"call %Outer @make_outer()",
		"extractvalue %Outer",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowerRuntimeBackedTypes(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
    count: mutable usize
    capacity: mutable usize

struct DynArrayView:
	items: mutable void&?
    count: mutable usize

extern take_array(values: darray[i32, row]) -> void
extern take_array_view(view: dview[i32]) -> usize
extern take_str(text: cstr[row]) -> void
`
	result := parseAndAnalyze(t, "backend_runtime_types.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"declare void @take_array(%DynArray__i32)",
		"declare i64 @take_array_view(%DynArrayView)",
		"declare void @take_str(ptr)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
