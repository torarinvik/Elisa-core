//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Shared preamble: a minimal cursor type so the R3 advance fixtures resolve cleanly and the
// negative assertions ("no flow warning") are meaningful rather than masked by type errors.
const flowCursorPreamble = `struct Cursor:
    pos: mutable usize

def cursor_end(c: Cursor&) -> bool:
    return c.pos >= 100

def cursor_char(c: Cursor&) -> char:
    return 'x'

def advance_char(c: lmut Cursor) -> void:
    c.pos <- c.pos + 1

`

func flowWarn(t *testing.T, name, src string) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{FlowLintMode: FlowLintWarn})
}

func flowStrict(t *testing.T, name, src string) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{FlowLintMode: FlowLintStrict})
}

func flowOff(t *testing.T, name, src string) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{})
}

// ---- R2: state-flag ban ----------------------------------------------------------------

// The canonical untyped-automaton shape: a bool `in_string` that is truth-tested and flipped in
// two branches. R2 must flag it — warning under -Wflow, error under strict, and it must name the
// binding and point at the enum rewrite.
const r2BoolFlagSrc = `def find_hash(source: darray[u8]&) -> i64:
    result: mutable i64 = -1
    i: mutable usize = 0
    while i < source.count |i, result, in_string: bool = false|:
        c: char = source[i].char()
        if in_string:
            if c == '"':
                in_string <- false
        elif c == '"':
            in_string <- true
        elif c == '#':
            result <- i.i64()
        i <- i + 1
    return result
`

func TestFlowR2BoolFlagWarnsThenStrictErrors(t *testing.T) {
	warn := flowWarn(t, "r2_bool_warn.elisa", r2BoolFlagSrc)
	all := allDiagnostics(warn)
	if !strings.Contains(all, "in_string") || !strings.Contains(all, "untyped state machine") {
		t.Fatalf("expected R2 warning naming in_string, got:\n%s", all)
	}
	if len(warn.Errors()) != 0 {
		t.Fatalf("R2 under -Wflow should warn, not error; got errors:\n%v", warn.Errors())
	}

	strict := flowStrict(t, "r2_bool_strict.elisa", r2BoolFlagSrc)
	if len(strict.Errors()) == 0 || !strings.Contains(strings.Join(strict.Errors(), "\n"), "in_string") {
		t.Fatalf("expected R2 hard error under strict mode, got errors:\n%v", strict.Errors())
	}
}

func TestFlowR2SilentWhenOff(t *testing.T) {
	off := flowOff(t, "r2_off.elisa", r2BoolFlagSrc)
	if strings.Contains(allDiagnostics(off), "untyped state machine") {
		t.Fatalf("flow lints must be silent when FlowLintMode is off, got:\n%s", allDiagnostics(off))
	}
}

// The ComplexFlow grant silences the covered loop at any level.
func TestFlowR2SilencedByGrant(t *testing.T) {
	src := `def find_hash(source: darray[u8]&) -> i64:
    result: mutable i64 = -1
    i: mutable usize = 0
    can ComplexFlow:
        while i < source.count |i, result, in_string: bool = false|:
            c: char = source[i].char()
            if in_string:
                if c == '"':
                    in_string <- false
            elif c == '"':
                in_string <- true
            elif c == '#':
                result <- i.i64()
            i <- i + 1
    return result
`
	got := flowStrict(t, "r2_grant.elisa", src)
	if strings.Contains(allDiagnostics(got), "untyped state machine") {
		t.Fatalf("can ComplexFlow: should silence R2, got:\n%s", allDiagnostics(got))
	}
}

// The FIXED shape — a typed enum mode matched structurally — must pass R2: an enum matched
// against its variants is not a literal comparison, so `mode` is never a flagged discriminant.
func TestFlowR2EnumModePasses(t *testing.T) {
	src := `const enum ScanMode of u8:
    Code
    Str
    StrEscape

def find_hash(source: darray[u8]&) -> i64:
    result: mutable i64 = -1
    i: mutable usize = 0
    while i < source.count |i, result, mode: ScanMode = ScanMode.Code|:
        c: char = source[i].char()
        match mode:
            ScanMode.Code:
                if c == '#':
                    result <- i.i64()
                if c == '"':
                    mode <- ScanMode.Str
            ScanMode.Str:
                if c == '\\':
                    mode <- ScanMode.StrEscape
                if c == '"':
                    mode <- ScanMode.Code
            ScanMode.StrEscape:
                mode <- ScanMode.Code
        i <- i + 1
    return result
`
	got := flowStrict(t, "r2_enum.elisa", src)
	if strings.Contains(allDiagnostics(got), "untyped state machine") {
		t.Fatalf("typed enum-mode loop must pass R2, got:\n%s", allDiagnostics(got))
	}
}

// A plain counter — incremented once, boundary-checked once — must pass R2. The single
// comparison site keeps an int binding below the ≥2 threshold, exactly as `read_fstring`'s brace
// `depth` does.
func TestFlowR2CounterPasses(t *testing.T) {
	src := `def count_until(source: darray[u8]&) -> usize:
    depth: mutable usize = 0
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
	got := flowStrict(t, "r2_counter.elisa", src)
	if strings.Contains(allDiagnostics(got), "untyped state machine") {
		t.Fatalf("a brace counter must pass R2 (single boundary check), got:\n%s", allDiagnostics(got))
	}
}

// A balanced bracket/nesting counter — `depth+1` on open, `depth-1` on close, checked `depth == 0`
// in several places — must pass R2. It clears the int thresholds (compared in ≥2 sites, stepped in
// ≥2 branches) but is a counter, not a discriminant. This is the dominant false-positive class
// found calibrating over the stage1 parser (docs/121 §5).
func TestFlowR2BalancedCounterPasses(t *testing.T) {
	src := `def scan_clause(source: darray[u8]&) -> usize:
    depth: mutable usize = 0
    i: mutable usize = 0
    hits: mutable usize = 0
    while i < source.count |i, depth, hits|:
        c: char = source[i].char()
        if c == '(':
            depth <- depth + 1
        elif c == ')':
            depth <- depth - 1
        elif depth == 0 and c == ',':
            hits <- hits + 1
        elif depth == 0 and c == ';':
            break
        i <- i + 1
    return hits
`
	got := flowStrict(t, "r2_balanced.elisa", src)
	if strings.Contains(allDiagnostics(got), "untyped state machine") {
		t.Fatalf("a balanced bracket counter must pass R2, got:\n%s", allDiagnostics(got))
	}
}

// A sticky one-way latch — a bool set true in multiple branches and never reset, recording a fact
// ("seen an explicit arg yet") — must pass R2. It is a boolean fact, not a toggled 2-state machine,
// so an enum rewrite would be wrong advice. Found calibrating over the stage1 parser.
func TestFlowR2StickyLatchPasses(t *testing.T) {
	src := `def has_explicit(source: darray[u8]&) -> bool:
    i: mutable usize = 0
    seen: mutable bool = false
    while i < source.count |i, seen|:
        c: char = source[i].char()
        if c == '=':
            if not seen:
                seen <- true
        elif c == ':':
            seen <- true
        i <- i + 1
    return seen
`
	got := flowStrict(t, "r2_sticky.elisa", src)
	if strings.Contains(allDiagnostics(got), "untyped state machine") {
		t.Fatalf("a sticky one-way latch must pass R2, got:\n%s", allDiagnostics(got))
	}
}

// ---- R3: duplicated advance / tail position --------------------------------------------

// The scattered-advance shape: `advance_char` in four branches, one branch advancing twice with
// work between. R3 must flag it and name the cursor.
func TestFlowR3ScatteredAdvanceWarnsThenStrictErrors(t *testing.T) {
	src := flowCursorPreamble + `def skip_block(cursor: lmut Cursor) -> void:
    while not cursor.cursor_end() |cursor|:
        if cursor.cursor_char() == '*':
            cursor <- cursor.advance_char()
            if cursor.cursor_char() == '/':
                cursor <- cursor.advance_char()
                return
        elif cursor.cursor_char() == '#':
            cursor <- cursor.advance_char()
            cursor <- cursor.advance_char()
        else:
            cursor <- cursor.advance_char()
`
	warn := flowWarn(t, "r3_scatter_warn.elisa", src)
	all := allDiagnostics(warn)
	if !strings.Contains(all, "cursor") || !strings.Contains(all, "advance") {
		t.Fatalf("expected R3 warning naming the cursor, got:\n%s", all)
	}
	if len(warn.Errors()) != 0 {
		t.Fatalf("R3 under -Wflow should warn, not error; got:\n%v", warn.Errors())
	}

	strict := flowStrict(t, "r3_scatter_strict.elisa", src)
	if len(strict.Errors()) == 0 {
		t.Fatalf("expected R3 hard error under strict mode, got:\n%v", strict.Errors())
	}
}

// The FIXED shape — decide the width, advance once in tail position — must pass R3.
func TestFlowR3SingleTailAdvancePasses(t *testing.T) {
	src := flowCursorPreamble + `def advance_chars(c: lmut Cursor, n: usize) -> void:
    c.pos <- c.pos + n

def skip_block(cursor: lmut Cursor) -> void:
    while not cursor.cursor_end() |cursor|:
        if cursor.cursor_char() == '*':
            cursor <- cursor.advance_chars(2)
            return
        width: usize = 2 if cursor.cursor_char() == '#' else 1
        cursor <- cursor.advance_chars(width)
`
	got := flowStrict(t, "r3_tail.elisa", src)
	if strings.Contains(allDiagnostics(got), "scattered advancement") {
		t.Fatalf("single tail-position advance must pass R3, got:\n%s", allDiagnostics(got))
	}
}

// A simple two-branch loop with one mid-branch advance must NOT trip R3.
func TestFlowR3SmallLoopPasses(t *testing.T) {
	src := flowCursorPreamble + `def skip_ws(cursor: lmut Cursor) -> void:
    while not cursor.cursor_end() |cursor|:
        if cursor.cursor_char() == ' ':
            cursor <- cursor.advance_char()
        else:
            return
`
	got := flowStrict(t, "r3_small.elisa", src)
	if strings.Contains(allDiagnostics(got), "double-skip") {
		t.Fatalf("a two-branch loop with one advance must pass R3, got:\n%s", allDiagnostics(got))
	}
}

// The GOLD STANDARD: a many-arm typed-mode scanner (the read_fstring rewrite R2 pushes toward)
// advances once per match arm — many arms, but each a single tail advance. R3 must NOT flag it;
// otherwise the two rules give contradictory advice. The escape arm advances twice but with a
// guard (`if at_end: return`) between, which is the legitimate escape-consumes-two idiom.
func TestFlowR3PerArmScannerPasses(t *testing.T) {
	src := flowCursorPreamble + `const enum Mode of u8:
    Text
    Expr

def scan(cursor: lmut Cursor) -> void:
    while not cursor.cursor_end() |cursor, mode: Mode = Mode.Text|:
        c: char = cursor.cursor_char()
        match mode:
            Mode.Text:
                if c == '{':
                    mode <- Mode.Expr
                    cursor <- cursor.advance_char()
                elif c == '\\':
                    cursor <- cursor.advance_char()
                    if cursor.cursor_end():
                        return
                    cursor <- cursor.advance_char()
                else:
                    cursor <- cursor.advance_char()
            Mode.Expr:
                if c == '}':
                    mode <- Mode.Text
                cursor <- cursor.advance_char()
`
	got := flowStrict(t, "r3_scanner.elisa", src)
	if strings.Contains(allDiagnostics(got), "double-skip") {
		t.Fatalf("a per-arm typed-mode scanner must pass R3 (no contradictory advice), got:\n%s", allDiagnostics(got))
	}
}

// A threaded-state accumulator folded over a collection — `table <- update(x, table)` twice in a
// row — must pass R3. The binding rides as a NON-receiver argument, so it is functional-state
// threading, not a cursor being double-advanced. Found calibrating over stage1 resolve_expr.
func TestFlowR3ThreadedAccumulatorPasses(t *testing.T) {
	src := `struct Table:
    n: mutable usize

def update(k: usize, t: Table) -> Table:
    return Table{n: t.n + k}

def fold(keys: darray[usize]&, values: darray[usize]&) -> Table:
    table: mutable Table = Table{n: 0}
    i: mutable usize = 0
    while i < keys.count |i, table|:
        table <- update(keys[i], table)
        table <- update(values[i], table)
        i <- i + 1
    return table
`
	got := flowStrict(t, "r3_accumulator.elisa", src)
	if strings.Contains(allDiagnostics(got), "double-skip") {
		t.Fatalf("a threaded-state accumulator must pass R3, got:\n%s", allDiagnostics(got))
	}
}
