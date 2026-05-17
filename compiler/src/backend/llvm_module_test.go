//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersModuleScopedStructsEnumsAndGlobals(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "module_codegen.elisa", `
module LaunchPipeline:
    struct State:
        code: i64

    enum Mode:
        Run
        Help

    global current: State = zeroed

    def make_state(code: i64) -> State:
        return State{code}

def read_code() -> i64:
    state = LaunchPipeline::make_state(7)
    return state.code
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, want := range []string{
		"%LaunchPipeline.State = type",
		"@LaunchPipeline.current",
		"@LaunchPipeline.make_state",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected generated IR to contain %q, got:\n%s", want, output)
		}
	}
}
