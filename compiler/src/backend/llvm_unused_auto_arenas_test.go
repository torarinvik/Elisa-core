package backend

import (
	"strings"
	"testing"
)

func TestLLVMElidesOnlyUnusedAutoArenaCleanup(t *testing.T) {
	const source = `
def buffer(x: i64) -> i64:
    can Memory.Allocate, Abort.Panic:
        values: darray[i64] = [x, x + 1]
        return values[1]

export fn buffer_value(x: i64) -> i64 = buffer
`
	result := parseAndAnalyzeBackendTest(t, "unused_auto_arena.elisa", source)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
	if err != nil {
		t.Fatal(err)
	}
	// The backing buffer uses a separate inferred arena. Its cleanup must
	// survive even though the enclosing function arena never gets used.
	if count := strings.Count(output, "call void @arena_free("); count != 1 {
		t.Fatalf("got %d arena frees, want only the buffer arena's cleanup:\n%s", count, output)
	}
}
