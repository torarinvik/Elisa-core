//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIREvaluatesOrMatchScrutineeOnce(t *testing.T) {
	src := `global mutable match_calls: i64 = 0

def next_code() -> i64:
    match_calls <- match_calls + 1
    return 2

def next_label() -> cstr:
    match_calls <- match_calls + 1
    return "beta"

def main() -> i64:
    result: i64 = match next_code():
        1 or 2: 7
        _: 0
    match next_label():
        "alpha" or "beta":
            match_calls <- match_calls + 10
        _:
            match_calls <- match_calls + 100
    return result + match_calls
`
	result := parseAndAnalyzeBackendTest(t, "or_match_scrutinee_once.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("scalar and string or-patterns should lower: %v", err)
	}
	if strings.Contains(output, "<null operand!") {
		t.Fatalf("or-pattern lowering emitted invalid LLVM IR:\n%s", output)
	}
	for _, function := range []string{"next_code", "next_label"} {
		// The only call to each side-effecting scrutinee belongs to main. Pattern
		// alternatives must compare the already evaluated LLVM value.
		count := 0
		for _, line := range strings.Split(output, "\n") {
			if strings.Contains(line, "call ") && strings.Contains(line, "@"+function+"(") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("or-pattern scrutinee %s was emitted %d times, want once:\n%s", function, count, output)
		}
	}
}
