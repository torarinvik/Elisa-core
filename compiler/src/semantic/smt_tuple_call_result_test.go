//go:build cgo

package semantic

import "testing"

// A tuple-valued returned call binds `result` to the call's projection token.
func TestSMTTupleCallResultBinding(t *testing.T) {
	src := `
def add(a: i64, b: i64) -> (known: bool, value: i64):
    ensure result.known == (b == 0)
    return (b == 0, 0)

def ar(a: i64, b: i64) -> (known: bool, value: i64):
    ensure result.known == add(a, b).known
    ensure result.value == add(a, b).value
    return add(a, b)

def ar2(a: i64, k: i64, b: i64) -> (known: bool, value: i64):
    ensure k == 1 implies result.value == add(a, b).value
    return add(a, b) if k == 1
    return (false, 0)


`
	result := analyzeContractStrict(t, "tuple_call_result.elisa", src)
	for _, name := range []string{"ar", "ar2"} {
		if n := ensureFailuresFor(result.Errors(), name); n != 0 {
			t.Fatalf("%s: expected proof, got %v", name, result.Errors())
		}
	}
	enumSrc := `
enum Op:
    Plus
    Minus
    Star

def add(a: i64, b: i64) -> (known: bool, value: i64):
    ensure result.known == (b == 0)
    return (b == 0, 0)

def sub(a: i64, b: i64) -> (known: bool, value: i64):
    ensure result.known == (b == 1)
    return (b == 1, 0)

def ar(a: i64, op: Op, b: i64) -> (known: bool, value: i64):
    ensure (op == Op.Plus) implies (result.known == add(a, b).known)
    ensure (op == Op.Minus) implies (result.known == sub(a, b).known)
    return add(a, b) if op == Op.Plus
    return sub(a, b) if op == Op.Minus
    return (false, 0)

`
	result = analyzeContractStrict(t, "tuple_call_enum.elisa", enumSrc)
	if n := ensureFailuresFor(result.Errors(), "ar"); n != 0 {
		t.Fatalf("enum ar: expected proof, got %v", result.Errors())
	}
	// SOUNDNESS: a call with swapped arguments is a different call.
	bad := `
def addx(a: i64, b: i64) -> (known: bool, value: i64):
    ensure result.known == (b == 0)
    return (b == 0, 0)

def swapped(a: i64, b: i64) -> (known: bool, value: i64):
    ensure result.known == addx(b, a).known
    return addx(a, b)

`
	result = analyzeContractStrict(t, "tuple_call_swapped.elisa", bad)
	if ensureFailuresFor(result.Errors(), "swapped") == 0 {
		t.Fatalf("swapped: false ensure must stay unproven; got %v", result.Errors())
	}
}
