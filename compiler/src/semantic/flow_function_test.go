//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/125 §6/§6b — function-scoped flow detectors: the strict block-`if` ban, shape
// re-test ladders, and shadow-prone elif tables.

// ---- strict block-`if` ban (§6b) ---------------------------------------------------------

const blockIfSrc = `def f(x: i64) -> i64:
    if x > 0:
        return 1
    return 0
`

// A written block `if` is fine under warn, an error under strict.
func TestStrictBlockIfBan(t *testing.T) {
	warn := flowWarn(t, "blockif_warn.elisa", blockIfSrc)
	if len(warn.Errors()) != 0 {
		t.Fatalf("block if must not error under -Wflow (warn), got:\n%v", warn.Errors())
	}
	if strings.Contains(allDiagnostics(warn), "block `if`") {
		t.Fatalf("block-if ban must be strict-only, got warning:\n%s", allDiagnostics(warn))
	}

	strict := flowStrict(t, "blockif_strict.elisa", blockIfSrc)
	all := strings.Join(strict.Errors(), "\n")
	if !strings.Contains(all, "block `if`") {
		t.Fatalf("expected strict block-if error, got:\n%v", strict.Errors())
	}
}

// A postfix guard desugars to an IfStmt but is NOT source syntax — never banned.
func TestStrictBlockIfExemptsPostfixGuard(t *testing.T) {
	src := `def f(x: i64) -> i64:
    i: mutable i64 = 0
    while i < 10 |i|:
        break if x > 0
        i <- i + 1
    return i
`
	strict := flowStrict(t, "blockif_postfix.elisa", src)
	if all := strings.Join(strict.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("postfix guard must not trip the block-if ban, got:\n%v", strict.Errors())
	}
}

// `can ComplexFlow:` is a pure acknowledgment grant — it must NOT join the function's
// inferred effect row, so callers never see a `can[ComplexFlow]` obligation.
func TestComplexFlowGrantDoesNotPropagate(t *testing.T) {
	src := `def helper(x: i64) -> i64:
    can ComplexFlow:
        if x > 0:
            return 2
    return 0

def caller(x: i64) -> i64:
    return helper(x)
`
	strict := flowStrict(t, "ackgrant.elisa", src)
	if all := allDiagnostics(strict); strings.Contains(all, "ComplexFlow]") || strings.Contains(all, "effect facts") {
		t.Fatalf("ComplexFlow grant must not propagate to callers, got:\n%s", all)
	}
	if len(strict.Errors()) != 0 {
		t.Fatalf("granted block-if must be clean under strict, got:\n%v", strict.Errors())
	}
}

// The bare optional bind `if maybe is value:` is the same checked-destructure family —
// it parses to OptionalBindExpr, not a TOKEN_IS BinaryExpr, and must be exempt too.
func TestStrictBlockIfExemptsOptionalBind(t *testing.T) {
	src := `def pick(x: i64) -> i64?:
    null return if x < 0
    return x

def f(x: i64) -> i64:
    maybe: i64? = pick(x)
    if maybe is value:
        return value
    return 0
`
	strict := flowStrict(t, "blockif_optbind.elisa", src)
	if all := strings.Join(strict.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("bare optional bind must not trip the block-if ban, got:\n%v", strict.Errors())
	}
}

// `if EXPR is PATTERN:` is a checked destructure — exempt (docs/80), including its else.
func TestStrictBlockIfExemptsIsBinding(t *testing.T) {
	src := `enum E:
    A
    B(i64)

def f(e: E) -> i64:
    if e is E.B(v):
        return v
    else:
        return 0
`
	strict := flowStrict(t, "blockif_is.elisa", src)
	if all := strings.Join(strict.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("if-is destructure must be exempt from the block-if ban, got:\n%v", strict.Errors())
	}
}

// `can ComplexFlow:` states the exception and silences the ban.
func TestStrictBlockIfComplexFlowGrant(t *testing.T) {
	src := `def f(x: i64) -> i64:
    can ComplexFlow:
        if x > 0:
            return 1
    return 0
`
	strict := flowStrict(t, "blockif_grant.elisa", src)
	if all := strings.Join(strict.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("can ComplexFlow must silence the block-if ban, got:\n%v", strict.Errors())
	}
}

// docs/125 §6b legitimate-guard exemption (ratified 2026-07-14): a block `if` whose branch
// BINDS a local and is straight-line (no elif, no nested if/match) is a conditional
// computation, not a hidden decision tree — exempt. But a binding-free block, an if/elif
// table, and a block whose body itself branches all STAY flagged.
func TestStrictBlockIfExemptsBindingGuard(t *testing.T) {
	exempt := `def f(x: i64) -> i64:
    if x > 0:
        y: i64 = x * 2
        return y
    return 0
`
	strict := flowStrict(t, "blockif_bindingguard.elisa", exempt)
	if all := strings.Join(strict.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("a straight-line binding guard block must be exempt, got:\n%v", strict.Errors())
	}

	// A binding-free MULTI-statement straight-line block is exempt (2026-07-14 broadening):
	// splitting it to per-statement postfix guards would re-evaluate the condition per
	// statement, so the block is the honest spelling.
	bindingFree := `def g(x: i64) -> i64:
    total: mutable i64 = 0
    if x > 0:
        total <- total + 1
        total <- total + 2
    return total
`
	s2 := flowStrict(t, "blockif_bindingfree.elisa", bindingFree)
	if all := strings.Join(s2.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("a binding-free multi-statement straight-line block must be exempt, got:\n%v", s2.Errors())
	}

	// A SINGLE-statement binding-free body stays flagged — it is exactly one postfix guard
	// with no condition duplication, so the fold pressure remains.
	singleStmt := `def g1(x: i64) -> i64:
    total: mutable i64 = 0
    if x > 0:
        total <- total + 1
    return total
`
	s2b := flowStrict(t, "blockif_singlestmt.elisa", singleStmt)
	if !strings.Contains(strings.Join(s2b.Errors(), "\n"), "block `if`") {
		t.Fatalf("a single-statement binding-free block must still be flagged, got:\n%v", s2b.Errors())
	}

	// A guarded loop (`if COND:` wrapping exactly one loop) is exempt — a loop is a
	// computation, not a decision, and hoisting the guard changes evaluation.
	loopGuard := `def g2(x: i64) -> i64:
    total: mutable i64 = 0
    if x > 0:
        for i in 0..<x:
            total <- total + i
    return total
`
	s2c := flowStrict(t, "blockif_loopguard.elisa", loopGuard)
	if all := strings.Join(s2c.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("a guarded loop must be exempt, got:\n%v", s2c.Errors())
	}

	// A plain if/else with both branches single-statement stays flagged — that is
	// ternary/value-if territory.
	evenAlt := `def g3(x: i64) -> i64:
    total: mutable i64 = 0
    if x > 0:
        total <- 1
    else:
        total <- 2
    return total
`
	s2d := flowStrict(t, "blockif_evenalt.elisa", evenAlt)
	if !strings.Contains(strings.Join(s2d.Errors(), "\n"), "block `if`") {
		t.Fatalf("a single/single if/else must still be flagged, got:\n%v", s2d.Errors())
	}

	// A plain if/else where one branch carries 2+ straight-line statements is exempt —
	// two-way effect alternation with no shared scrutinee has no when/match/ternary form.
	unevenAlt := `def g4(x: i64) -> i64:
    total: mutable i64 = 0
    if x > 0:
        total <- total + 1
        total <- total + 2
    else:
        total <- 9
    return total
`
	s2e := flowStrict(t, "blockif_unevenalt.elisa", unevenAlt)
	if all := strings.Join(s2e.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("an uneven straight-line if/else alternation must be exempt, got:\n%v", s2e.Errors())
	}

	// An if/elif chain is a decision table — stays flagged even when the then-branch binds.
	table := `def h(x: i64) -> i64:
    r: mutable i64 = 0
    if x > 0:
        y: i64 = x
        r <- y
    elif x < 0:
        r <- 0 - 1
    return r
`
	s3 := flowStrict(t, "blockif_table.elisa", table)
	if !strings.Contains(strings.Join(s3.Errors(), "\n"), "block `if`") {
		t.Fatalf("an if/elif table must still be flagged, got:\n%v", s3.Errors())
	}

	// A body that itself branches is a decision TREE — stays flagged even though it binds.
	tree := `def k(x: i64) -> i64:
    if x > 0:
        y: i64 = x
        if y > 5:
            return y
    return 0
`
	s4 := flowStrict(t, "blockif_tree.elisa", tree)
	if !strings.Contains(strings.Join(s4.Errors(), "\n"), "block `if`") {
		t.Fatalf("a nested-decision (tree) block must still be flagged, got:\n%v", s4.Errors())
	}

	// A `rebind`-destructure guard binds new locals (a declaring TupleBind) — same irreducible
	// principle as `=`, so it is exempt.
	rebindGuard := `def m(x: i64) -> i64:
    if x > 0:
        rebind a, b = (x, x + 1)
        return a + b
    return 0
`
	s5 := flowStrict(t, "blockif_rebindguard.elisa", rebindGuard)
	if all := strings.Join(s5.Errors(), "\n"); strings.Contains(all, "block `if`") {
		t.Fatalf("a rebind-destructure binding guard must be exempt, got:\n%v", s5.Errors())
	}
}

// ---- shape re-tests (§6) -----------------------------------------------------------------

const shapeRetestPreamble = `enum Expr2:
    IntLit(i64)
    Unary(i64, Expr2)
    Paren(Expr2)

`

// Three nested if-is probes, each re-probing a value the previous pattern bound.
func TestShapeRetestLadderWarns(t *testing.T) {
	src := shapeRetestPreamble + `def f(e: Expr2) -> i64:
    if e is Expr2.Unary(op, operand):
        if operand is Expr2.Paren(inner):
            if inner is Expr2.IntLit(v):
                return v
    return 0
`
	warn := flowWarn(t, "retest_warn.elisa", src)
	all := allDiagnostics(warn)
	if !strings.Contains(all, "one deep pattern") {
		t.Fatalf("expected shape-retest warning, got:\n%s", all)
	}
}

// Two levels stay under the threshold; a hand-written match is never counted.
func TestShapeRetestBelowThresholdSilent(t *testing.T) {
	src := shapeRetestPreamble + `def f(e: Expr2) -> i64:
    if e is Expr2.Unary(op, operand):
        if operand is Expr2.IntLit(v):
            return v
    return 0

def g(e: Expr2) -> i64:
    return match e:
        Expr2.Unary(op, operand):
            match operand:
                Expr2.IntLit(v):
                    v
                _:
                    0
        _:
            0
`
	warn := flowWarn(t, "retest_silent.elisa", src)
	if all := allDiagnostics(warn); strings.Contains(all, "one deep pattern") {
		t.Fatalf("2-deep ladder / hand-written match must stay silent, got:\n%s", all)
	}
}

// Probing a DIFFERENT value (not bound by the outer pattern) is not a re-test chain.
func TestShapeRetestUnrelatedProbeSilent(t *testing.T) {
	src := shapeRetestPreamble + `def f(e: Expr2, other: Expr2) -> i64:
    if e is Expr2.Unary(op, operand):
        if other is Expr2.Paren(inner):
            if inner is Expr2.IntLit(v):
                return v
    return 0
`
	warn := flowWarn(t, "retest_unrelated.elisa", src)
	if all := allDiagnostics(warn); strings.Contains(all, "one deep pattern") {
		t.Fatalf("probe of an unrelated value must not chain, got:\n%s", all)
	}
}

// ---- shadow-prone elif tables (§6) --------------------------------------------------------

// A 4-arm string-equality ladder over one scrutinee is a decision table.
func TestShadowElifTableWarns(t *testing.T) {
	src := `def f(name: sview) -> i64:
    if name == "a":
        return 1
    elif name == "b":
        return 2
    elif name == "c" or name == "d":
        return 3
    elif name == "e":
        return 4
    return 0
`
	warn := flowWarn(t, "table_warn.elisa", src)
	all := allDiagnostics(warn)
	if !strings.Contains(all, "decision table") || !strings.Contains(all, "name") {
		t.Fatalf("expected shadow-elif-table warning naming the scrutinee, got:\n%s", all)
	}
	if len(warn.Errors()) != 0 {
		t.Fatalf("table detector must warn (not error) under -Wflow, got:\n%v", warn.Errors())
	}
}

// Mixed scrutinees, non-equality conditions, and short ladders stay silent.
func TestShadowElifTableSilentCases(t *testing.T) {
	src := `def mixed(a: i64, b: i64) -> i64:
    if a == 1:
        return 1
    elif b == 2:
        return 2
    elif a == 3:
        return 3
    return 0

def ranges(a: i64) -> i64:
    if a == 1:
        return 1
    elif a > 2:
        return 2
    elif a == 3:
        return 3
    return 0

def short_ladder(a: i64) -> i64:
    if a == 1:
        return 1
    elif a == 2:
        return 2
    return 0
`
	warn := flowWarn(t, "table_silent.elisa", src)
	if all := allDiagnostics(warn); strings.Contains(all, "decision table") {
		t.Fatalf("mixed/non-equality/short ladders must stay silent, got:\n%s", all)
	}
}

// Comparing against a member reference or another variable is NOT a table (v1 literal-only).
func TestShadowElifTableNonLiteralSilent(t *testing.T) {
	src := `struct Pair:
    kind: i64

def f(p: Pair&, q: Pair&) -> i64:
    if p.kind == q.kind:
        return 1
    elif p.kind == 2:
        return 2
    elif p.kind == 3:
        return 3
    return 0
`
	warn := flowWarn(t, "table_nonliteral.elisa", src)
	if all := allDiagnostics(warn); strings.Contains(all, "decision table") {
		t.Fatalf("field-vs-field head must break the chain (3 literal arms remain: 2,3 = below threshold), got:\n%s", all)
	}
}

// A ladder testing one scrutinee against payload-less enum tags IS a table — `when` rows
// accept bare tags (whenAtomTag), so the rewrite advice holds.
func TestShadowElifTableEnumTags(t *testing.T) {
	src := `enum Kind:
    A
    B
    C
    D

def f(k: Kind) -> i64:
    if k == Kind.A:
        return 1
    elif k == Kind.B:
        return 2
    elif k == Kind.C or k == Kind.D:
        return 3
    return 0
`
	warn := flowWarn(t, "table_enumtag.elisa", src)
	all := allDiagnostics(warn)
	if !strings.Contains(all, "decision table") || !strings.Contains(all, "`k`") {
		t.Fatalf("expected shadow-elif-table warning for enum-tag ladder, got:\n%s", all)
	}
}

// The RHS must be a MEMBER REFERENCE: a variable field that merely has enum type
// (`other.kind`) is a computed value — `when` cannot express it (R3), so no warning.
func TestShadowElifTableEnumFieldSilent(t *testing.T) {
	src := `enum Kind:
    A
    B
    C

struct Tok:
    kind: Kind

def f(k: Kind, a: Tok&, b: Tok&, c: Tok&) -> i64:
    if k == a.kind:
        return 1
    elif k == b.kind:
        return 2
    elif k == c.kind:
        return 3
    return 0
`
	warn := flowWarn(t, "table_enumfield.elisa", src)
	if all := allDiagnostics(warn); strings.Contains(all, "decision table") {
		t.Fatalf("enum-typed variable fields are computed values, must stay silent, got:\n%s", all)
	}
}
