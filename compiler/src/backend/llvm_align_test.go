//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"strings"
	"testing"
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

// @cacheline_aligned was removed as redundant with @align(64); it now reports a
// migration message pointing at the general form.
func TestAnalyzeCachelineAlignedStructIsRejected(t *testing.T) {
	result := analyzeInlineTestSource(t, "cacheline_removed.elisa", `@cacheline_aligned
struct Counter:
    value: i64
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "@cacheline_aligned has been removed; use @align(64)") {
		t.Fatalf("expected cacheline_aligned removal diagnostic, got:\n%s", errText)
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
	result := parseAndAnalyzeBackendTest(t, "backend_layout_introspection.elisa", `struct Header layout(c):
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
	result := parseAndAnalyzeBackendTest(t, "backend_layout_introspection_generic_style.elisa", `struct Header layout(c):
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
	result := parseAndAnalyzeBackendTest(t, "backend_static_assert_layout_introspection.elisa", `struct Header layout(c):
    tag: u8
    count: u32
    payload: u64

def keep() -> void:
    static assert:
        size_of[Header]() == 16
        align_of[Header] == 8
        offset_of[Header](.payload) == 8
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIRChecksAbiLayoutAnnotation(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_abi_layout_annotation.elisa", `@abi_layout(size, 16, align, 8, field, tag, 0, field, count, 4, field, payload, 8)
struct Header layout(c):
    tag: u8
    count: u32
    payload: u64

def keep() -> void:
    pass
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIRChecksAbiLayoutAnnotationOnFixedLayout(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_abi_layout_fixed_layout_annotation.elisa", `@fixed_layout
@abi_layout(size, 16, align, 8, field, tag, 0, field, count, 4, field, payload, 8)
struct Header:
    tag: u8
    count: u32
    payload: u64

def keep() -> void:
    pass
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIRFoldsStaticReflection(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_reflection.elisa", `enum Maybe:
    None
    Some(value: i64)

struct Pair:
    left: i64
    right: bool

static def score() -> i64:
    total: mutable i64 = 0
    for variant in variants(Maybe):
        total <- total + variant.field_count
        if variant.has_field("value"):
            total <- total + variant.tag
    for field in fields(Pair) where field.name != "left":
        total <- total + field.index
    return total

def folded() -> i64:
    static assert:
        variants(Maybe).count == 2
        fields(Pair).count == 2
        score() == 3
    return 3
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	if !strings.Contains(output, "ret i64 3") {
		t.Fatalf("expected folded static reflection result, got IR:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersStaticGeneratedFunctions(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_generate.elisa", `enum Expr:
    Int(value: i64)
    Bool(value: bool)

static generate:
    for variant in variants(Expr):
        emit def is_${variant.name}(expr: Expr) -> bool:
            return expr is Expr.${variant.name}

def folded(expr: Expr) -> bool:
    return is_Int(expr) or is_Bool(expr)
`)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@is_Int") || !strings.Contains(output, "@is_Bool") {
		t.Fatalf("expected generated functions in IR, got:\n%s", output)
	}
}

func TestGenerateLLVMIRChecksStaticBlockWithLayoutIntrospection(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_block_layout_introspection.elisa", `struct Header layout(c):
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
	result := parseAndAnalyzeBackendTest(t, "backend_top_level_static_assert_layout_introspection.elisa", `struct Header layout(c):
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

func TestGenerateLLVMIRRejectsAbiLayoutAnnotationMismatch(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_abi_layout_annotation_bad.elisa", `@abi_layout(size, 12, field, payload, 4)
struct Header layout(c):
    tag: u8
    count: u32
    payload: u64

def keep() -> void:
    pass
`)
	_, err := GenerateLLVMIR(result)
	if err == nil || !strings.Contains(err.Error(), `@abi_layout on struct "Header" expected size 12 bytes, got 16`) {
		t.Fatalf("expected abi layout failure, got: %v", err)
	}
}

func TestGenerateLLVMIRRejectsStaticAssertWithLayoutIntrospection(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_assert_layout_introspection_bad.elisa", `struct Header layout(c):
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
	result := parseAndAnalyzeBackendTest(t, "backend_top_level_static_assert_layout_introspection_bad.elisa", `struct Header layout(c):
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
struct Counter layout(c):
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
