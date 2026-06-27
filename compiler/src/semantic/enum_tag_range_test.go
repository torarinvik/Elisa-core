package semantic

import (
	"strings"
	"testing"
)

// COMPLETENESS: a plain enum's tag cast to u32 is provably in [0, variantCount-1], so indexing
// an array[T, N] where N == variantCount must elide the bounds check entirely.
func TestPlainEnumTagU32IndexProvesArrayBounds(t *testing.T) {
	src := `
enum Color:
    Red
    Green
    Blue

def lookup(c: Color, table: array[u32, 3]&) -> u32:
    return table[c.u32()]
`
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "enum_tag_array.elisa", src, AnalyzeOptions{EnforceUnsafePermissions: true})
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "unchecked index requires") {
		t.Fatalf("plain-enum tag .u32() into array[u32,3] must prove in bounds, got:\n%s", all)
	}
}

// COMPLETENESS: u64 postfix cast also carries the tag range.
func TestPlainEnumTagU64IndexProvesArrayBounds(t *testing.T) {
	src := `
enum Dir:
    North
    South
    East
    West

def lookup(d: Dir, table: array[i64, 4]&) -> i64:
    return table[d.u64()]
`
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "enum_tag_array_u64.elisa", src, AnalyzeOptions{EnforceUnsafePermissions: true})
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "unchecked index requires") {
		t.Fatalf("plain-enum tag .u64() into array[i64,4] must prove in bounds, got:\n%s", all)
	}
}

// SOUNDNESS (tag range tight): an enum with N variants has tags 0..N-1.
// Indexing into array[T, N-1] (one slot too small) must NOT be proven in bounds.
func TestPlainEnumTagIndexTooBigDeclines(t *testing.T) {
	src := `
enum Color:
    Red
    Green
    Blue

def lookup(c: Color, table: array[u32, 2]&) -> u32:
    return table[c.u32()]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "enum_tag_too_big.elisa", src, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("enum tag range [0,2] does not fit array[u32,2]; bounds check must remain, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS (packed enum declined): a packed enum's tag is embedded in a bitfield word;
// the numeric value of a packed-enum cast is NOT the sequential ordinal and must not be
// treated as in-range without a refinement type.
func TestPackedEnumTagIndexDeclines(t *testing.T) {
	src := `
packed enum Op:
    Add
    Sub
    Mul

def lookup(op: Op, table: array[u32, 3]&) -> u32:
    return table[op.u32()]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "packed_enum_tag.elisa", src, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("packed-enum tag must NOT be auto-ranged; bounds check must remain, got:\n%s", allDiagnostics(result))
	}
}
