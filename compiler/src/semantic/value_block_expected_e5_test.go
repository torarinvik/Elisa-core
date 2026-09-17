//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/119 §2: a value block's tail IS the value, so it is checked against the
// destination exactly as the bare expression would be. Before, the block analyzed its
// tail with no expected type: `s: sview = "long"` was accepted while the same literal as
// a block tail was "expects sview, got static u8&", and a `null` tail into `i64?` was
// "ternary branches are incompatible: i64 and null".
func TestValueBlockTailTakesDestinationType(t *testing.T) {
	src := `def f(n: i64) -> i64:
    s: sview =
        pick: bool = n > 2
        "long" if pick else "no"
    o: i64? =
        ok: bool = n > 0
        n if ok else null
    b: u8 =
        k: i64 = n
        200
    x: f32 =
        h: i64 = n
        1.5
    label: sview =
        if n > 1:
            t: i64 = n
            "big"
        else:
            "small"
    total: i64 = s.len.i64() + label.len.i64()
    if o is v:
        return v + total + b.i64()
    return total
`
	analyzeTreeTestSource(t, "value_block_expected.elisa", src)
}

// The destination type sharpens a literal; it must not hide a real mismatch or an
// out-of-range literal.
func TestValueBlockTailMismatchStillReported(t *testing.T) {
	src := `def f(n: i64) -> i64:
    v: i64 =
        a: i64 = n
        true
    small: i8 =
        b: i64 = n
        1000
    return v
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "value_block_mismatch.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	for _, want := range []string{`variable "v" expects i64, got bool`, "integer literal 1000 does not fit in i8"} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected %q, got:\n%s", want, all)
		}
	}
}

// docs/119 §2.2 rule 3 / E5: `break`/`continue` may not jump out of a value block to a
// loop outside it. Previously accepted.
func TestValueBlockBreakContinueE5(t *testing.T) {
	src := `def f(xs: darray[i64]) -> i64:
    for x in xs:
        v: i64 =
            break if x > 1
            x
        w: i64 =
            continue if x > 2
            x
    s: i64 = for x in xs |acc: i64 = 0| -> acc:
        inner: i64 =
            break if x > 3
            x
        acc <- acc + inner
    return s
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "value_block_e5.elisa", src)
	errs := result.Errors()
	all := strings.Join(errs, "\n")
	if got := strings.Count(all, "`break` may not jump out of a value block (docs/119 E5)"); got != 2 {
		t.Fatalf("expected 2 break E5 errors, got %d:\n%s", got, all)
	}
	if got := strings.Count(all, "`continue` may not jump out of a value block (docs/119 E5)"); got != 1 {
		t.Fatalf("expected 1 continue E5 error, got %d:\n%s", got, all)
	}
}

// A loop expression owns its interior `break`/`continue`, and so does any loop opened
// inside a value block; neither is E5. A break with no loop at all keeps its own error.
func TestValueBlockOwnLoopsMayBreak(t *testing.T) {
	src := `def f(xs: darray[i64]) -> i64:
    s: i64 = for x in xs |acc: i64 = 0| -> acc:
        continue if x < 0
        break if x > 100
        acc <- acc + x
    t: i64 =
        count: mutable i64 = 0
        for y in xs:
            break if y > 50
            continue if y < 0
            count <- count + 1
        count
    for x in xs:
        u: i64 = for y in xs |c: i64 = 0| -> c:
            break if y == x
            c <- c + 1
        break if u > 3
    return s + t
`
	analyzeTreeTestSource(t, "value_block_own_loops.elisa", src)

	bare := analyzeTreeTestSourceWithSemanticErrors(t, "value_block_no_loop.elisa", `def f(n: i64) -> i64:
    v: i64 =
        break if n > 1
        n
    return v
`)
	all := strings.Join(bare.Errors(), "\n")
	if !strings.Contains(all, "break is only valid inside a loop") || strings.Contains(all, "E5") {
		t.Fatalf("a break with no enclosing loop keeps its own error, got:\n%s", all)
	}
}
