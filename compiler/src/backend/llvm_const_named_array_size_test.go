//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// `T[NAME]` parses as an array only when NAME looks constant-styled (`MAX_N`); `i64[LIMIT]`
// arrives as a generic application the analyzer already reads as an array. The backend reported
// `unknown type "LIMIT"` until it applied the same reading.
func TestGenerateLLVMIRLowersArrayWithUnderscorelessConstSize(t *testing.T) {
	src := `const LIMIT: usize = 10
const N: usize = 3

def tally(seed: i64) -> i64:
    flags: mutable bool[LIMIT] = zeroed
    values: mutable i64[N] = zeroed
    flags[2] <- seed > 0
    values[1] <- seed
    if flags[2]:
        return values[1] + 1
    values[1]
`
	result := parseAndAnalyzeBackendTest(t, "const_named_array_size.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"[10 x i1]", "[3 x i64]"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected the lowered locals to include %q, got:\n%s", check, output)
		}
	}
}
