package main

import (
	"bytes"
	"strings"
	"testing"
)

// Two-binder comprehension heads (`for k, v in src`) destructure a tuple-yielding source —
// the expression-level mirror of the statement `for k, v in d:`. This pins the value semantics
// over a dict source for all three comprehension forms (list, dict, set), plus a filtered head.
func TestComprehensionTwoBinderRuntime(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def check(cond: bool, msg: cstr) -> void can[Abort.Panic]:
    if not cond:
        panic(msg)

def scenarios() -> i64 can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        d: mutable dict[i64, i64] = {}
        d.put(1, 10)
        d.put(2, 20)
        d.put(3, 30)

        # list comprehension: both binders usable in the value
        pairs: darray[i64] = [k * 100 + v for k, v in d]
        sum: mutable i64 = 0
        for p in pairs:
            sum <- sum + p
        check(pairs.count == 3 and sum == 660, "list-two-binder")

        # filtered two-binder head
        big: darray[i64] = [v for k, v in d if k > 1]
        check(big.count == 2 and big[0] + big[1] == 50, "filtered-two-binder")

        # dict comprehension: flip key/value
        flipped: dict[i64, i64] = {v: k for k, v in d}
        check(flipped.count == 3, "dict-two-binder-count")
        if flipped.get(20) is fk:
            check(fk == 2, "dict-two-binder-value")
        else:
            panic("dict-two-binder-missing")

        # set comprehension over both binders
        ks: set[i64] = {k + v for k, v in d}
        check(ks.count == 3 and ks.contains(33), "set-two-binder")

        # discard binders work in either position
        keys: darray[i64] = [k for k, _ in d]
        vals: darray[i64] = [v for _, v in d]
        check(keys.count == 3 and vals.count == 3, "discard-binder")

        return 29

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return scenarios().int()
`)
	if strings.Contains(out, "two-binder") || strings.Contains(out, "discard-binder") {
		t.Fatalf("two-binder comprehension produced a wrong result: status=%s out=%q", status, out)
	}
	if status != "RUNERR" || !strings.Contains(out, "exit status 29") {
		t.Fatalf("expected clean exit code 29, got status=%s out=%q", status, out)
	}
}

// A two-binder head over a range is a compile error (a range yields one integer per step).
func TestComprehensionTwoBinderRangeRejected(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	src := "def build() -> usize:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        xs: darray[int] = [a + b for a, b in 0..<5]\n" +
		"        return xs.count\n"
	_, _, ok := analyzeProgram("two_binder_range.elisa", []byte(src), &stderr)
	if ok {
		t.Fatalf("expected two-binder range comprehension to be rejected")
	}
	if !strings.Contains(stderr.String(), "tuple-yielding source") {
		t.Fatalf("expected the tuple-yielding-source diagnostic, got:\n%s", stderr.String())
	}
}
