//go:build cgo

package semantic

import "testing"

// Leading guarded literal returns of a tuple-valued callee describe the call's result.
func TestSMTGuardPrefixReturns(t *testing.T) {
	src := `
def ref(text: sview) -> (valid: bool, value: i64):
    return (false, 0) if text.len <= 0
    return (true, 7) if text.len == 1
    return (true, text.len)

def ps(text: sview) -> (valid: bool, value: i64):
    ensure result.valid == ref(text).valid
    ensure result.value == ref(text).value
    return (false, 0) if text.len <= 0
    return (true, 7) if text.len == 1
    return (true, text.len)

def psbad(text: sview) -> (valid: bool, value: i64):
    ensure result.value == ref(text).value
    return (false, 0) if text.len <= 0
    return (true, 8) if text.len == 1
    return (true, text.len)


def ordered(n: i64) -> (valid: bool, value: i64):
    return (true, 1) if n <= 5
    return (true, 2) if n <= 3
    return (false, 0)

def order_bad(n: i64) -> i64:
    requires n == 2
    ensure result == ordered(n).value
    return 2

def order_ok(n: i64) -> i64:
    requires n == 2
    ensure result == ordered(n).value
    return 1

def after_local(n: i64) -> (valid: bool, value: i64):
    k: i64 = 4
    return (true, k) if n <= 5
    return (false, 0)

def local_bad(n: i64) -> i64:
    requires n == 2
    ensure result == after_local(n).value
    return 9

`
	result := analyzeContractStrict(t, "guard_prefix.elisa", src)
	for _, name := range []string{"ps", "order_ok"} {
		if n := ensureFailuresFor(result.Errors(), name); n != 0 {
			t.Fatalf("%s: expected proof, got %v", name, result.Errors())
		}
	}
	// SOUNDNESS: a wrong literal, a later guard shadowed by an earlier one, and a body that
	// starts with a statement other than a guarded return.
	for _, name := range []string{"psbad", "order_bad", "local_bad"} {
		if ensureFailuresFor(result.Errors(), name) == 0 {
			t.Fatalf("%s: false ensure must stay unproven; got %v", name, result.Errors())
		}
	}
}
