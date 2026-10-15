//go:build cgo

package semantic

import "testing"

const smtEnumDecls = `
struct P:
    a: i64
    b: i64

enum E:
    Invalid
    Lit(v: i64, p: P)
    Id(n: i64, p: P)

enum G:
    A
    B
    C

def peq(l: P, r: P) -> bool:
    return l.a == r.a and l.b == r.b
`

func requireProven(t *testing.T, name, src string, fns ...string) {
	t.Helper()
	result := analyzeContractStrict(t, name, src)
	for _, fn := range fns {
		if n := ensureFailuresFor(result.Errors(), fn); n != 0 {
			t.Fatalf("%s must prove; got: %v", fn, result.Errors())
		}
	}
}

func requireUnproven(t *testing.T, name, src string, fns ...string) {
	t.Helper()
	result := analyzeContractStrict(t, name, src)
	for _, fn := range fns {
		if ensureFailuresFor(result.Errors(), fn) == 0 {
			t.Fatalf("%s must stay unproven; got: %v", fn, result.Errors())
		}
	}
}

// Variant tags and payloads: an ensure that is a match over an enum, proven from a statement match,
// a match expression, an if-chain over payload-free variants, and a fall-through after a `pass` arm.
func TestSMTEnumVariantMatchEnsures(t *testing.T) {
	src := smtEnumDecls + `
def stmt_match(e: E) -> i64:
    ensure match e:
        E.Invalid: result == 0
        E.Lit(v, _): result == v
        E.Id(v, _): result == v
    match e:
        E.Invalid:
            return 0
        E.Lit(v, _):
            return v
        E.Id(v, _):
            return v

def expr_match(e: E) -> i64:
    ensure match e:
        E.Invalid: result == 0
        E.Lit(v, _): result == v
        E.Id(v, _): result == v
    return match e:
        E.Invalid: 0
        E.Lit(v, _): v
        E.Id(v, _): v

def struct_payload(e: E) -> P:
    ensure match e:
        E.Invalid: peq(result, P{a: 0, b: 0})
        E.Lit(_, q): peq(result, q)
        E.Id(_, q): peq(result, q)
    match e:
        E.Invalid:
            pass
        E.Lit(_, q):
            return q
        E.Id(_, q):
            return q
    return P{a: 0, b: 0}

def if_chain(e: G) -> i64:
    ensure e == G.A implies result == 0
    ensure e == G.B implies result == 1
    ensure e == G.C implies result == 2
    return 0 if e == G.A
    return 1 if e == G.B
    return 2

def alternation(e: G) -> i64:
    ensure e == G.C implies result == 2
    ensure e != G.C implies result == 1
    match e:
        G.A | G.B:
            return 1
        G.C:
            return 2
`
	requireProven(t, "enum_match_ensures.elisa", src, "stmt_match", "expr_match", "struct_payload", "if_chain", "alternation")
}

// SOUNDNESS: each of these is false for some input, so it must stay unproven.
//   - wrong_payload: the Id arm returns v + 1;
//   - wrong_default: G.C reaches the wildcard arm returning 1;
//   - wrong_alt: G.B is in the first alternation, returning 1;
//   - shadowed: an inner `v` shadows the payload binder and is returned instead (both share an SMT
//     name; the binder/payload equality must not attach to the shadowing local);
//   - mutated: the subject is reassigned inside the arm, so the arm's tag fact must not survive;
//   - payload_eq: equality of two payload-carrying enum values is not tag equality.
func TestSMTEnumVariantMatchSoundness(t *testing.T) {
	src := smtEnumDecls + `
def wrong_payload(e: E) -> i64:
    ensure match e:
        E.Invalid: result == 0
        E.Lit(v, _): result == v
        E.Id(v, _): result == v
    match e:
        E.Invalid:
            return 0
        E.Lit(v, _):
            return v
        E.Id(v, _):
            return v + 1

def wrong_default(e: G) -> i64:
    ensure e == G.C implies result == 2
    match e:
        G.A:
            return 0
        _:
            return 1

def wrong_alt(e: G) -> i64:
    ensure e == G.B implies result == 2
    match e:
        G.A | G.B:
            return 1
        G.C:
            return 2

def shadowed(e: E, c: bool) -> i64:
    ensure match e:
        E.Invalid: result == 0
        E.Lit(v, _): result == v
        E.Id(v, _): result == v
    match e:
        E.Invalid:
            return 0
        E.Lit(v, _):
            if c:
                v: i64 = 7
                return v
            return v
        E.Id(v, _):
            return v

def mutated(e0: G) -> i64:
    ensure result == 0
    e: mutable G = e0
    match e:
        G.A:
            e <- G.B
            return 0 if e == G.A
            return 1
        _:
            return 0

def payload_eq(a: E, b: E) -> bool:
    ensure result
    return a == b
`
	requireUnproven(t, "enum_match_soundness.elisa", src, "wrong_payload", "wrong_default", "wrong_alt", "shadowed", "mutated", "payload_eq")
}

// Const-enum matches: an exhaustive `return match` without a wildcard, and module-qualified member
// patterns in a statement match (their path facts used to be dropped by a spelling comparison).
func TestSMTConstEnumMatchForms(t *testing.T) {
	src := `
module Other:
    public:
        const enum Kind of u32:
            A
            B
            C

module Here:
    public:
        const enum Act of u8:
            X
            Y
            Z

        def exhaustive_expr(k: Act) -> u32:
            ensure k != Act.X or result == 10
            ensure k != Act.Y or result == 20
            ensure k != Act.Z or result == 30
            return match k:
                Act.X: 10
                Act.Y: 20
                Act.Z: 30

        def qualified_stmt(kind: Other::Kind, p: bool) -> Act:
            ensure kind == Other::Kind.A implies result == Act.X
            return Act.Y if p and kind != Other::Kind.A
            match kind:
                Other::Kind.A:
                    return Act.X
                _:
                    return Act.Z

        def wrong_qualified(kind: Other::Kind) -> Act:
            ensure kind == Other::Kind.B implies result == Act.X
            match kind:
                Other::Kind.A:
                    return Act.X
                _:
                    return Act.Z

        def wrong_expr(k: Act) -> u32:
            ensure k != Act.Z or result == 30
            return match k:
                Act.X: 10
                Act.Y: 30
                Act.Z: 20
`
	requireProven(t, "const_enum_forms.elisa", src, "exhaustive_expr", "qualified_stmt")
	requireUnproven(t, "const_enum_forms_bad.elisa", src, "wrong_qualified", "wrong_expr")
}

// SOUNDNESS: while lowering a callee's body, a field read of the callee's own formal must not resolve
// to a name-keyed symbol — that aliased the CALLER's same-named variable, so `peq(x, y)` was
// "proven" true from the caller's unrelated `requires l.a == r.a and l.b == r.b`.
func TestSMTCalleeFormalDoesNotAliasCallerVariable(t *testing.T) {
	src := `
struct P:
    a: i64
    b: i64

def peq(l: P, r: P) -> bool:
    return l.a == r.a and l.b == r.b

def aliased(l: P, r: P, x: P, y: P) -> bool:
    requires l.a == r.a and l.b == r.b
    ensure result
    return peq(x, y)

def reflexive(x: P) -> P:
    ensure peq(result, x)
    return x
`
	requireUnproven(t, "callee_alias_bad.elisa", src, "aliased")
	requireProven(t, "callee_alias_ok.elisa", src, "reflexive")
}
