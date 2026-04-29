//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersRegularEnumValuesWithoutArenaAlloc(t *testing.T) {
	src := `enum Small:
    Int(value: i64)
    Pair(left: i64, right: i64)
    Done

def make_node(value: i64) -> Small:
    return Small.Int(value)

def score(node: Small) -> i64:
    match node:
        Small.Int(value):
            return value
        Small.Pair(left, right):
            return left + right
        Small.Done:
            return 0

def total(seed: i64) -> i64:
    items: array[Small, 3] = [Small.Int(1), make_node(seed), Small.Done]
    maybe: Small? = Small.Pair(2, 3)
    total: i64 = score(items[0]) + score(items[1]) + score(items[2])
    if let node = maybe:
        total = total + score(node)
    return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_regular_enum_values.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Small = type { i32, [2 x i64] }",
		"define %Small @make_node(i64",
		"define i64 @score(%Small",
		"define i64 @total(i64",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected regular enum value lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@arena_alloc") {
		t.Fatalf("expected regular enum value lowering to avoid arena allocation helpers, got:\n%s", output)
	}
}
