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

func TestGenerateCHeaderRendersAlignedStructAndGlobal(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "header_align.elisa", `@align(64)
struct Counter:
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
