//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestEffectGrantedGlobalArrayReadUsesDirectAddress(t *testing.T) {
	src := `global mutable probe_array: array[u32, 1048576]
def read_index(i: usize) -> u32 can[Global.Read, Abort.Panic]:
    return (probe_array can Global.Read)[i]
export fn read_value(i: usize) -> u32 = read_index
`
	result := parseAndAnalyzeBackendTest(t, "global_effect_index.elisa", src)
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
		ir, err := GenerateLLVMIRWithOpt(result, level)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(ir, "llvm.memcpy") || strings.Contains(ir, "addr.tmp") {
			t.Fatalf("global array element read at %v materialized an array copy:\n%s", level, ir)
		}
		if !strings.Contains(ir, "@probe_array") || !strings.Contains(ir, "load i32") {
			t.Fatalf("missing global element load at %v:\n%s", level, ir)
		}
	}
}
