//go:build cgo

package backend

import (
	"strings"
	"testing"

	"elisacore/src/semantic"
)

func TestAnalyzeStructAlignAnnotationSetsTypeMetadata(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "align_semantics.elisa", `@align(32)
struct Vec4:
    x: f32
    y: f32
    z: f32
    w: f32
`)
	st, ok := result.NamedTypes["Vec4"].(*semantic.StructType)
	if !ok || st == nil {
		t.Fatalf("expected Vec4 to resolve to semantic.StructType, got %#v", result.NamedTypes["Vec4"])
	}
	if !st.HasAlignment || st.Alignment != 32 {
		t.Fatalf("expected Vec4 alignment metadata to record 32 bytes, got %+v", st)
	}
}

func TestAnalyzeCachelineAlignedStructSetsTypeMetadata(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "cacheline_semantics.elisa", `@cacheline_aligned
struct Counter:
    value: i64
`)
	st, ok := result.NamedTypes["Counter"].(*semantic.StructType)
	if !ok || st == nil {
		t.Fatalf("expected Counter to resolve to semantic.StructType, got %#v", result.NamedTypes["Counter"])
	}
	if !st.HasAlignment || st.Alignment != 64 {
		t.Fatalf("expected Counter alignment metadata to record 64 bytes, got %+v", st)
	}
}

func TestAnalyzeStructAlignAnnotationRejectsNonPowerOfTwo(t *testing.T) {
	result := analyzeInlineTestSource(t, "align_invalid.elisa", `@align(24)
struct Vec4:
    x: f32
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, `@align on struct "Vec4" expects a positive power-of-two byte alignment, got "24"`) {
		t.Fatalf("expected invalid align diagnostic, got:\n%s", errText)
	}
}

func TestAnalyzeCachelineAlignedStructRejectsArguments(t *testing.T) {
	result := analyzeInlineTestSource(t, "cacheline_invalid.elisa", `@cacheline_aligned(128)
struct Counter:
    value: i64
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, `@cacheline_aligned on struct "Counter" does not take arguments`) {
		t.Fatalf("expected invalid cacheline_aligned diagnostic, got:\n%s", errText)
	}
}

func TestGenerateLLVMIRAppliesStructAlignmentToGlobalsAndAllocas(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_align.elisa", `@align(64)
struct Counter:
    value: i64

global counter: Counter = zeroed

def fold() -> i64:
    local: Counter = zeroed
    return local.value
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	if !strings.Contains(output, "@counter = global %Counter zeroinitializer, align 64") {
		t.Fatalf("expected global counter to carry align 64, got IR:\n%s", output)
	}
	if !strings.Contains(output, "%local = alloca %Counter, align 64") {
		t.Fatalf("expected local Counter alloca to carry align 64, got IR:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersLayoutIntrospectionBuiltins(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_layout_introspection.elisa", `struct Header layout c:
    tag: u8
    count: u32
    payload: u64

def layout_total() -> usize:
    return size_of(Header) + align_of(Header) + offset_of(Header, count) + offset_of(Header, payload)
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	if !strings.Contains(output, "ret i64 36") {
		t.Fatalf("expected layout_total to fold size/align/offsets to 36, got IR:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersGenericStyleLayoutIntrospectionBuiltins(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_layout_introspection_generic_style.elisa", `struct Header layout c:
    tag: u8
    count: u32
    payload: u64

def layout_total() -> usize:
    return size_of[Header]() + align_of[Header] + offset_of[Header](.count) + offset_of[Header](payload)
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	if !strings.Contains(output, "ret i64 36") {
		t.Fatalf("expected generic-style layout_total to fold size/align/offsets to 36, got IR:\n%s", output)
	}
}

func TestGenerateLLVMIRChecksStaticAssertWithLayoutIntrospection(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_assert_layout_introspection.elisa", `struct Header layout c:
    tag: u8
    count: u32
    payload: u64

def keep() -> void:
    static assert size_of[Header]() == 16
    static assert align_of[Header] == 8
    static assert offset_of[Header](.payload) == 8
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIRChecksStaticBlockWithLayoutIntrospection(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_block_layout_introspection.elisa", `struct Header layout c:
    tag: u8
    count: u32
    payload: u64

def keep() -> void:
    static:
        assert size_of[Header]() == 16
        if true:
            assert offset_of[Header](.payload) == 8
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIRChecksTopLevelStaticAssertWithLayoutIntrospection(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_top_level_static_assert_layout_introspection.elisa", `struct Header layout c:
    tag: u8
    count: u32
    payload: u64

static assert size_of[Header]() == 16
static assert align_of[Header] == 8
static assert offset_of[Header](.payload) == 8

def keep() -> void:
    pass
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIRSkipsStaticFunctionDecls(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "static_def_skip.elisa", `static def answer() -> i64:
    return 42

def keep() -> i64:
    return 0
`)
	ir, err := GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	if strings.Contains(ir, "@answer") {
		t.Fatalf("expected static function to be compile-time-only, got IR:\n%s", ir)
	}
}

func TestGenerateLLVMIRChecksStaticAssertCallingStaticFunction(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_assert.elisa", `static def answer(step: i64) -> i64:
    return step + 40

def keep() -> void:
    static assert answer(2) == 42
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticExpressionStatement(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_stmt.elisa", `static def answer(step: i64) -> i64:
    return step + 40

def keep() -> void:
    static answer(2)
    static:
        answer(2)
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticLocalsAndIf(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_locals.elisa", `static def answer(step: i64) -> i64:
    value: mutable i64 = step + 40
    if value == 42:
        return value
    return 0

def keep() -> void:
    static assert answer(2) == 42
    static:
        local: i64 = answer(2)
        assert local == 42
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticLoops(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_loops.elisa", `static def sum_to(limit: i64) -> i64:
    total: mutable i64 = 0
    for i in 0..<limit:
        total <- total + i
    while total < 10:
        total <- total + 1
    return total

def keep() -> void:
    static assert sum_to(4) == 10
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticNamedAndDefaultArgs(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_named_defaults.elisa", `static def answer(step: i64, base: i64 = 40) -> i64:
    return base + step

def keep() -> void:
    static assert answer(step: 2) == 42
    static assert answer(base: 41, step: 1) == 42
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticMatch(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_match.elisa", `const enum Op of i32:
    ADD = 1
    SUB = 2
    MUL = 3

static def classify(op: Op) -> i64:
    match op:
        Op.ADD | Op.SUB:
            return 1
        _:
            return 3

def keep() -> void:
    static assert classify(Op.ADD) == 1
    static assert classify(Op.SUB) == 1
    static assert classify(Op.MUL) == 3
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticVoidFunctionFallthrough(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_void_fallthrough.elisa", `static def note() -> void:
    value: i64 = 42

def keep() -> void:
    static note()
    static:
        note()
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIRRejectsStaticAssertWithLayoutIntrospection(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_assert_layout_introspection_bad.elisa", `struct Header layout c:
    tag: u8
    count: u32
    payload: u64

def keep() -> void:
    static assert size_of[Header]() == 12, "Header ABI changed"
`)
	_, err := GenerateLLVMIR(result)
	if err == nil || !strings.Contains(err.Error(), "static assert failed: Header ABI changed") {
		t.Fatalf("expected backend static assert failure, got: %v", err)
	}
}

func TestGenerateLLVMIRRejectsTopLevelStaticAssertWithLayoutIntrospection(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_top_level_static_assert_layout_introspection_bad.elisa", `struct Header layout c:
    tag: u8
    count: u32
    payload: u64

static assert size_of[Header]() == 12, "Header ABI changed"

def keep() -> void:
    pass
`)
	_, err := GenerateLLVMIR(result)
	if err == nil || !strings.Contains(err.Error(), "static assert failed: Header ABI changed") {
		t.Fatalf("expected backend top-level static assert failure, got: %v", err)
	}
}

func TestGenerateCHeaderRendersAlignedStructAndGlobal(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "header_align.elisa", `@align(64)
struct Counter layout c:
    value: i64

global counter: Counter = zeroed
export global counter
`)
	header, err := GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	if !strings.Contains(header, "struct Counter {\n    int64_t value;\n} __attribute__((aligned(64)));") {
		t.Fatalf("expected aligned struct definition in header, got:\n%s", header)
	}
	if !strings.Contains(header, "extern Counter counter __attribute__((aligned(64)));") {
		t.Fatalf("expected aligned exported global declaration in header, got:\n%s", header)
	}
}
