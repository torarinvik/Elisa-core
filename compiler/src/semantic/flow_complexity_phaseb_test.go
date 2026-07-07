//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ============================================================================================
// Phase B — R1 (nesting budget), R4 (exit budget), R5 (progress), R6 (single mutation).
// Shares the flowWarn/flowStrict/flowOff helpers and flowCursorPreamble from
// flow_complexity_test.go (same package).
// ============================================================================================

// ---- R1: nesting budget ----------------------------------------------------------------

// Four levels of stacked `if` nesting inside the loop body — arrow code hiding a state machine.
// R1 fires above depth 3 (calibration: real branchy code nests pattern guards 3 deep cleanly), so
// this depth-4 body must warn under -Wflow and error under strict.
const r1DeepSrc = `def deep_scan(source: darray[u8]&) -> i64:
    result: mutable i64 = -1
    i: mutable usize = 0
    while i < source.count |i, result|:
        c: char = source[i].char()
        if c == '#':
            if result < 0:
                if i > 5:
                    if i < 90:
                        result <- i.i64()
        i <- i + 1
    return result
`

func TestFlowR1DeepNestingWarnsThenStrictErrors(t *testing.T) {
	warn := flowWarn(t, "r1_deep_warn.elisa", r1DeepSrc)
	all := allDiagnostics(warn)
	if !strings.Contains(all, "conditional nesting deeper than 3") {
		t.Fatalf("expected R1 nesting warning, got:\n%s", all)
	}
	if len(warn.Errors()) != 0 {
		t.Fatalf("R1 under -Wflow should warn, not error; got:\n%v", warn.Errors())
	}
	strict := flowStrict(t, "r1_deep_strict.elisa", r1DeepSrc)
	if len(strict.Errors()) == 0 || !strings.Contains(strings.Join(strict.Errors(), "\n"), "conditional nesting") {
		t.Fatalf("expected R1 hard error under strict mode, got:\n%v", strict.Errors())
	}
}

// Two levels of nesting is within budget — must pass.
func TestFlowR1TwoLevelsPasses(t *testing.T) {
	src := `def two_level(source: darray[u8]&) -> i64:
    result: mutable i64 = -1
    i: mutable usize = 0
    while i < source.count |i, result|:
        c: char = source[i].char()
        if c == '#':
            if result < 0:
                result <- i.i64()
        i <- i + 1
    return result
`
	got := flowStrict(t, "r1_two.elisa", src)
	if strings.Contains(allDiagnostics(got), "conditional nesting") {
		t.Fatalf("two-level nesting must pass R1, got:\n%s", allDiagnostics(got))
	}
}

// The read_fstring shape: `match mode:` (one level) with a single trailing guard per arm. The
// carve-out keeps each arm's terminal `if` free, so total depth is 1 — must pass R1.
func TestFlowR1MatchTrailingGuardPasses(t *testing.T) {
	src := `const enum Mode of u8:
    Text
    Expr

def scan_modes(source: darray[u8]&) -> void:
    i: mutable usize = 0
    while i < source.count |i, mode: Mode = Mode.Text|:
        c: char = source[i].char()
        match mode:
            Mode.Text:
                if c == '{':
                    mode <- Mode.Expr
            Mode.Expr:
                if c == '}':
                    mode <- Mode.Text
        i <- i + 1
`
	got := flowStrict(t, "r1_trailing.elisa", src)
	if strings.Contains(allDiagnostics(got), "conditional nesting") {
		t.Fatalf("a match with per-arm trailing guards must pass R1, got:\n%s", allDiagnostics(got))
	}
}

// When R2 fires on a loop, R1 is suppressed — the enum rewrite R2 prescribes collapses the
// nesting too, so a single (R2) diagnostic is emitted, not two (docs/121 §7).
func TestFlowR1SuppressedWhenR2Fires(t *testing.T) {
	src := `def suppressed(source: darray[u8]&) -> i64:
    result: mutable i64 = -1
    i: mutable usize = 0
    while i < source.count |i, result, in_string: bool = false|:
        c: char = source[i].char()
        if in_string:
            if c == '"':
                if result < 0:
                    in_string <- false
        elif c == '"':
            in_string <- true
        elif c == '#':
            result <- i.i64()
        i <- i + 1
    return result
`
	got := flowWarn(t, "r1_suppressed.elisa", src)
	all := allDiagnostics(got)
	if !strings.Contains(all, "untyped state machine") {
		t.Fatalf("expected R2 to fire on the flag, got:\n%s", all)
	}
	if strings.Contains(all, "conditional nesting") {
		t.Fatalf("R1 must be suppressed when R2 fires on the same loop, got:\n%s", all)
	}
}

// ---- R4: exit budget -------------------------------------------------------------------

// Three structurally different exits — a converted index, and two distinct integer sentinels.
// R4 must flag it and, because two exits are int literals, sharpen with the sentinel advice.
const r4ManyExitsSrc = `def scan_hash(source: darray[u8]&) -> i64:
    i: mutable usize = 0
    while i < source.count |i|:
        c: char = source[i].char()
        if c == '#':
            return i.i64()
        elif c == '!':
            return -1
        elif c == '?':
            return 0
        i <- i + 1
    return -2
`

func TestFlowR4ManyExitShapesWarnsThenStrictErrors(t *testing.T) {
	warn := flowWarn(t, "r4_many_warn.elisa", r4ManyExitsSrc)
	all := allDiagnostics(warn)
	if !strings.Contains(all, "structurally different results") {
		t.Fatalf("expected R4 exit-budget warning, got:\n%s", all)
	}
	if !strings.Contains(all, "integer sentinels") {
		t.Fatalf("expected R4 sentinel-sharpening hint (two int-literal exits), got:\n%s", all)
	}
	if len(warn.Errors()) != 0 {
		t.Fatalf("R4 under -Wflow should warn, not error; got:\n%v", warn.Errors())
	}
	strict := flowStrict(t, "r4_many_strict.elisa", r4ManyExitsSrc)
	if len(strict.Errors()) == 0 {
		t.Fatalf("expected R4 hard error under strict mode, got:\n%v", strict.Errors())
	}
}

// Two exit shapes is within budget — must pass at any count.
func TestFlowR4TwoShapesPasses(t *testing.T) {
	src := `def two_exits(source: darray[u8]&) -> i64:
    i: mutable usize = 0
    while i < source.count |i|:
        c: char = source[i].char()
        if c == '#':
            return i.i64()
        elif c == '!':
            return -1
        i <- i + 1
    return -1
`
	got := flowStrict(t, "r4_two.elisa", src)
	if strings.Contains(allDiagnostics(got), "structurally different results") {
		t.Fatalf("two exit shapes must pass R4, got:\n%s", allDiagnostics(got))
	}
}

// The ComplexFlow grant silences R4.
func TestFlowR4SilencedByGrant(t *testing.T) {
	src := `def scan_hash(source: darray[u8]&) -> i64:
    i: mutable usize = 0
    can ComplexFlow:
        while i < source.count |i|:
            c: char = source[i].char()
            if c == '#':
                return i.i64()
            elif c == '!':
                return -1
            elif c == '?':
                return 0
            i <- i + 1
    return -2
`
	got := flowStrict(t, "r4_grant.elisa", src)
	if strings.Contains(allDiagnostics(got), "structurally different results") {
		t.Fatalf("can ComplexFlow: should silence R4, got:\n%s", allDiagnostics(got))
	}
}

// ---- R5: per-path progress -------------------------------------------------------------

// A `while` with an inert `else` branch: on the non-space path the cursor never advances and the
// loop can spin forever. R5 must flag that branch.
const r5InertSrc = `def spin(source: darray[u8]&) -> void:
    i: mutable usize = 0
    while i < source.count |i|:
        c: char = source[i].char()
        if c == ' ':
            i <- i + 1
        else:
            pass
`

func TestFlowR5InertBranchWarnsThenStrictErrors(t *testing.T) {
	warn := flowWarn(t, "r5_inert_warn.elisa", r5InertSrc)
	all := allDiagnostics(warn)
	if !strings.Contains(all, "spin forever") {
		t.Fatalf("expected R5 progress warning, got:\n%s", all)
	}
	if len(warn.Errors()) != 0 {
		t.Fatalf("R5 under -Wflow should warn, not error; got:\n%v", warn.Errors())
	}
	strict := flowStrict(t, "r5_inert_strict.elisa", r5InertSrc)
	if len(strict.Errors()) == 0 {
		t.Fatalf("expected R5 hard error under strict mode, got:\n%v", strict.Errors())
	}
}

// Every branch advances the cursor — no path can hang, so R5 must pass even without a top-level
// unconditional advance.
func TestFlowR5AllBranchesAdvancePasses(t *testing.T) {
	src := `def scan_ws(source: darray[u8]&) -> void:
    i: mutable usize = 0
    while i < source.count |i|:
        c: char = source[i].char()
        if c == ' ':
            i <- i + 1
        else:
            i <- i + 2
`
	got := flowStrict(t, "r5_advance.elisa", src)
	if strings.Contains(allDiagnostics(got), "spin forever") {
		t.Fatalf("a loop whose every branch advances must pass R5, got:\n%s", allDiagnostics(got))
	}
}

// R5 is `while`-only; a structurally bounded `for` loop with an inert branch is not its concern.
func TestFlowR5ForLoopExempt(t *testing.T) {
	src := `def bounded(source: darray[u8]&) -> void:
    for i in 0..<source.count:
        c: char = source[i].char()
        if c == ' ':
            pass
`
	got := flowStrict(t, "r5_for.elisa", src)
	if strings.Contains(allDiagnostics(got), "spin forever") {
		t.Fatalf("a for loop must be exempt from R5, got:\n%s", allDiagnostics(got))
	}
}

// ---- R6: single mutation per path (warn-only) ------------------------------------------

// Two SEPARATE sequential `if` branches both mutating `depth`: on the path where both fire it
// changes twice, coupled to the order the branches were written. R6 must warn — and, because it
// is pinned at warn-tier, it must warn even under strict mode without failing the build.
const r6SequentialSrc = `def measure(source: darray[u8]&) -> i64:
    depth: mutable i64 = 0
    i: mutable usize = 0
    while i < source.count |i, depth|:
        c: char = source[i].char()
        if c == '(':
            depth <- depth + 1
        if c == ')':
            depth <- depth - 1
        i <- i + 1
    return depth
`

func TestFlowR6SequentialMutationsWarnEvenInStrict(t *testing.T) {
	warn := flowWarn(t, "r6_seq_warn.elisa", r6SequentialSrc)
	if !strings.Contains(allDiagnostics(warn), "separate branches in sequence") {
		t.Fatalf("expected R6 warning, got:\n%s", allDiagnostics(warn))
	}

	strict := flowStrict(t, "r6_seq_strict.elisa", r6SequentialSrc)
	all := allDiagnostics(strict)
	if !strings.Contains(all, "separate branches in sequence") {
		t.Fatalf("R6 must still warn under strict mode, got:\n%s", all)
	}
	if strings.Contains(strings.Join(strict.Errors(), "\n"), "separate branches in sequence") {
		t.Fatalf("R6 is warn-only and must never become a strict-mode error, got errors:\n%v", strict.Errors())
	}
}

func TestFlowR6SilentWhenOff(t *testing.T) {
	off := flowOff(t, "r6_off.elisa", r6SequentialSrc)
	if strings.Contains(allDiagnostics(off), "separate branches in sequence") {
		t.Fatalf("R6 must be silent when flow lints are off, got:\n%s", allDiagnostics(off))
	}
}

// A single branch (one `if`-`elif` chain) that mutates the binding is the balanced-counter shape —
// only one branch runs per iteration, so there is no order-coupled double mutation. Must pass R6.
func TestFlowR6SingleBranchPasses(t *testing.T) {
	src := `def measure_one(source: darray[u8]&) -> i64:
    depth: mutable i64 = 0
    i: mutable usize = 0
    while i < source.count |i, depth|:
        c: char = source[i].char()
        if c == '(':
            depth <- depth + 1
        elif c == ')':
            depth <- depth - 1
        i <- i + 1
    return depth
`
	got := flowWarn(t, "r6_single.elisa", src)
	if strings.Contains(allDiagnostics(got), "separate branches in sequence") {
		t.Fatalf("a single if-elif branch must pass R6, got:\n%s", allDiagnostics(got))
	}
}
