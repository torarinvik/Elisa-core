//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// Under -ftrace the backend must emit a call to elisa_trace_record at every statement,
// passing a pointer to the function's name -- the instrumentation that feeds the bounded
// "rewind a few steps" ring buffer. Without the flag, no such calls appear.
func TestGenerateLLVMIREmitsTraceCallsWhenEnabled(t *testing.T) {
	src := `
def step(a: i32) -> i32:
	y: i32 = a + 1
	return y * 2

def main() -> i32:
	return step(3)
`
	withTrace := parseAndAnalyzeBackendTest(t, "trace_on.elisa", src)
	g, err := compileLLVMModuleWithTargetDebugTrace(withTrace, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, true)
	if err != nil {
		t.Fatalf("compile with -ftrace returned error: %v", err)
	}
	defer g.dispose()
	ir := g.printModule()

	if !strings.Contains(ir, "elisa_trace_record") {
		t.Fatalf("expected calls to elisa_trace_record under -ftrace, got:\n%s", ir)
	}
	// One record call per statement: step has 2, main has 1 => at least 3 calls.
	if n := strings.Count(ir, "@elisa_trace_record"); n < 3 {
		t.Fatalf("expected >=3 record calls (one per statement), got %d:\n%s", n, ir)
	}
	// The function name must be materialized as a string constant.
	if !strings.Contains(ir, "step") || !strings.Contains(ir, "main") {
		t.Fatalf("expected function-name string constants in the module, got:\n%s", ir)
	}
	// Value capture: the scalar local `y` must produce a value record.
	if !strings.Contains(ir, "elisa_trace_record_value") {
		t.Fatalf("expected a value record for the scalar declaration, got:\n%s", ir)
	}

	withoutTrace := parseAndAnalyzeBackendTest(t, "trace_off.elisa", src)
	g2, err := compileLLVMModuleWithTarget(withoutTrace, OptimizationLevel0, DefaultPackedLoweringProfile(), "")
	if err != nil {
		t.Fatalf("compile returned error: %v", err)
	}
	defer g2.dispose()
	if strings.Contains(g2.printModule(), "elisa_trace_record") {
		t.Fatalf("did not expect trace calls without -ftrace")
	}
}
