//go:build cgo

package semantic

import "testing"

const smtLiteralReevalDecls = `
struct Tok:
    line: u32
    column: u32
    length: u32
    text: sview

struct P:
    line: u32
    column: u32
    end_column: u32

struct Cell:
    v: mutable i64

def bump(c: mutable Cell&) -> i64:
    requires c.v < 1000
    c.v <- c.v + 1
    return 0
`

// A returned literal's field is its construction argument when that argument can be evaluated again
// at the return: field reads through a reference and arithmetic over them, when no component of the
// literal has side effects; and a component evaluated AFTER the last side effect.
func TestSMTLiteralFieldArgumentReevaluation(t *testing.T) {
	src := smtLiteralReevalDecls + `
def pos_of(token: Tok&) -> P:
    ensure result.line == token.line
    ensure result.end_column == token.column + token.length
    return P{line: token.line, column: token.column, end_column: token.column + token.length}

def pos_of_val(token: Tok) -> P:
    ensure result.line == token.line
    return P{line: token.line, column: token.column, end_column: token.column + token.length}

def snap_after(c: mutable Cell&) -> (a: i64, b: i64):
    requires c.v < 1000
    ensure result.b == c.v
    return (bump(c), c.v)
`
	result := analyzeContractStrict(t, "literal_reeval.elisa", src)
	for _, fn := range []string{"pos_of", "pos_of_val", "snap_after"} {
		if ensureFailuresFor(result.Errors(), fn) != 0 {
			t.Fatalf("%s must prove; got: %v", fn, result.Errors())
		}
	}
}

// SOUNDNESS: a component read BEFORE a later component's side effect captured the old value.
// `(c.v, bump(c))` was proven to satisfy `result.a == c.v` although bump increments c.v.
func TestSMTLiteralComponentBeforeSideEffectIsNotReevaluated(t *testing.T) {
	src := smtLiteralReevalDecls + `
def snap(c: mutable Cell&) -> (a: i64, b: i64):
    requires c.v < 1000
    ensure result.a == c.v
    return (c.v, bump(c))

def snap_struct(c: mutable Cell&) -> Cell:
    requires c.v < 1000
    ensure result.v == c.v
    return Cell{v: c.v + bump(c)}

def bump0(c: mutable Cell&) -> i64:
    requires c.v < 1000
    ensure result == 0
    c.v <- c.v + 1
    return 0

def in_struct(c: mutable Cell&) -> Cell:
    requires c.v < 1000
    ensure result.v == c.v
    return Cell{v: c.v + bump0(c)}

def in_tuple_last(c: mutable Cell&) -> (a: i64, b: i64):
    requires c.v < 1000
    ensure result.b == c.v
    return (0, c.v + bump0(c))
`
	result := analyzeContractStrict(t, "literal_reeval_bad.elisa", src)
	for _, fn := range []string{"snap", "snap_struct", "in_struct", "in_tuple_last"} {
		if ensureFailuresFor(result.Errors(), fn) == 0 {
			t.Fatalf("%s must stay unproven; got: %v", fn, result.Errors())
		}
	}
}

// A projection THROUGH a literal component reads the component's field: `(false, fact).next.v` is
// `fact.v` (stage1's ownership_binding_consume shape), for tuple and struct literals.
func TestSMTNestedProjectionThroughLiteralComponent(t *testing.T) {
	src := smtLiteralReevalDecls + `
struct Fact:
    reachable: bool
    v: i64

struct Step:
    known: bool
    next: Fact

def keep(fact: Fact) -> (known: bool, next: Fact):
    ensure result.next.reachable == fact.reachable
    ensure result.next.v == fact.v
    return (false, fact)

def keep_struct(fact: Fact) -> Step:
    ensure result.next.v == fact.v
    return Step{known: true, next: fact}
`
	result := analyzeContractStrict(t, "literal_nested.elisa", src)
	for _, fn := range []string{"keep", "keep_struct"} {
		if ensureFailuresFor(result.Errors(), fn) != 0 {
			t.Fatalf("%s must prove; got: %v", fn, result.Errors())
		}
	}
}

// SOUNDNESS: the component is a struct VALUE captured before a later component mutates its source;
// `(c, bump(c)).first.v` is the old c.v, not the current one.
func TestSMTNestedProjectionDeclinesComponentBeforeSideEffect(t *testing.T) {
	src := smtLiteralReevalDecls + `
def snap_cell(c: mutable Cell&) -> (first: Cell, b: i64):
    requires c.v < 1000
    ensure result.first.v == c.v
    return (c, bump(c))
`
	result := analyzeContractStrict(t, "literal_nested_effect.elisa", src)
	if ensureFailuresFor(result.Errors(), "snap_cell") == 0 {
		t.Fatalf("bump(c) runs after c is captured: result.first.v is the old c.v; got: %v", result.Errors())
	}
}
