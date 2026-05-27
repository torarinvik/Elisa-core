//go:build cgo

package backend

import (
	"strings"
	"testing"

	"elisacore/src/semantic"
)

func TestAnalyzeSegmentAgnosticAnnotationSetsFunctionMetadata(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "segment_agnostic_semantics.elisa", `
@segment_agnostic
def handler() -> int:
    return 7
`)
	sym, ok := result.GlobalScope.Lookup("handler")
	if !ok {
		t.Fatal("expected handler symbol")
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil {
		t.Fatalf("expected handler to resolve to semantic.FuncType, got %#v", sym.Type)
	}
	if !fnType.HasSegmentAgnostic {
		t.Fatalf("expected handler segment-agnostic metadata to be recorded, got %+v", fnType)
	}
}

func TestGenerateLLVMIRMarksSegmentAgnosticFunctions(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "segment_agnostic_backend.elisa", `
@segment_agnostic
def handler() -> int:
    return 7
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	attrs := functionAttributeGroupForTest(t, output, "handler")
	if !strings.Contains(attrs, `"elisacore.segment_agnostic"="true"`) {
		t.Fatalf("expected handler to carry segment-agnostic backend marker, got attributes {%s}\nIR:\n%s", attrs, output)
	}
	if strings.Contains(attrs, "ssp") {
		t.Fatalf("segment-agnostic handler must not carry stack-protector attrs, got attributes {%s}\nIR:\n%s", attrs, output)
	}
}

func TestSegmentAgnosticIRValidatorRejectsStackProtector(t *testing.T) {
	ir := `
define i32 @handler() #0 {
entry:
  ret i32 0
}

attributes #0 = { sspstrong "elisacore.segment_agnostic"="true" }
`
	err := validateSegmentAgnosticLLVMIR(ir)
	if err == nil || !strings.Contains(err.Error(), "hidden Segment.Host dependency") {
		t.Fatalf("expected segment-agnostic stack-protector lowering to be rejected, got %v", err)
	}
}

func TestSegmentAgnosticIRValidatorRejectsLiteralSegmentAccess(t *testing.T) {
	ir := `
define i32 @handler() #0 {
entry:
  call void asm sideeffect "movq %fs:0x28, %rax", ""()
  ret i32 0
}

attributes #0 = { "elisacore.segment_agnostic"="true" }
`
	err := validateSegmentAgnosticLLVMIR(ir)
	if err == nil || !strings.Contains(err.Error(), "literal %fs/%gs access") {
		t.Fatalf("expected segment-agnostic literal segment access to be rejected, got %v", err)
	}
}
