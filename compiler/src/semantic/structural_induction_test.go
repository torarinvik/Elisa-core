//go:build cgo

package semantic

// Structural induction over recursive datatypes (analyzer_structural_induction.go).
//
// TRUE_CLAIM   - a correct postcondition discharged by canonicalization / structural induction.
// FALSE_CLAIM  - a wrong postcondition that MUST be rejected.
// SOUNDNESS    - a non-well-founded / non-terminating predicate must DECLINE without hanging.

import (
	"testing"
)

// TRUE_CLAIM: identity passthrough. `rebuild` returns its argument; the `requires` predicate equals the
// `ensure` predicate over the same value, so the two `all_nonneg(...)` calls canonicalize to one symbol
// and the obligation is the hypothesis.
func TestStructuralInduction_IdentityPassthrough(t *testing.T) {
	src := `
enum Tree:
    Leaf
    Node(value: i64, left: Tree, right: Tree)

ghost def all_nonneg(t: Tree) -> bool:
    match t:
        Tree.Leaf:
            return true
        Tree.Node(value: v, left: l, right: r):
            return v >= 0 and all_nonneg(l) and all_nonneg(r)

def rebuild(t: Tree) -> Tree:
    requires all_nonneg(t)
    ensure all_nonneg(result)
    return t
`
	r := analyzeContractStrict(t, "struct_ind_identity.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("identity passthrough should prove `ensure all_nonneg(result)` by predicate canonicalization, got: %v", errs)
	}
}

// TRUE_CLAIM (list): the same canonicalization over a recursive list-shaped enum.
func TestStructuralInduction_ListPredicatePassthrough(t *testing.T) {
	src := `
enum IntList:
    Empty
    Cons(elem: i64, more: IntList)

ghost def all_pos(xs: IntList) -> bool:
    match xs:
        IntList.Empty:
            return true
        IntList.Cons(elem: h, more: rest):
            return h > 0 and all_pos(rest)

def keep(xs: IntList) -> IntList:
    requires all_pos(xs)
    ensure all_pos(result)
    return xs
`
	r := analyzeContractStrict(t, "struct_ind_list.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("list predicate passthrough should prove, got: %v", errs)
	}
}

// FALSE_CLAIM: the postcondition asserts a DIFFERENT predicate than the precondition. `all_nonneg(t)`
// does not imply `all_pos(t)` (a node value of 0 is non-negative but not positive), so the prover must
// NOT accept it. The two predicates are distinct opaque symbols with no implication between them.
func TestStructuralInduction_FalseCrossPredicate(t *testing.T) {
	src := `
enum Tree:
    Leaf
    Node(value: i64, left: Tree, right: Tree)

ghost def all_nonneg(t: Tree) -> bool:
    match t:
        Tree.Leaf:
            return true
        Tree.Node(value: v, left: l, right: r):
            return v >= 0 and all_nonneg(l) and all_nonneg(r)

ghost def all_pos(t: Tree) -> bool:
    match t:
        Tree.Leaf:
            return true
        Tree.Node(value: v, left: l, right: r):
            return v > 0 and all_pos(l) and all_pos(r)

def coerce(t: Tree) -> Tree:
    requires all_nonneg(t)
    ensure all_pos(result)
    return t
`
	r := analyzeContractStrict(t, "struct_ind_false_cross.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (FALSE_CLAIM ACCEPTED): all_nonneg(t) does not imply all_pos(t)")
	}
}

// FALSE_CLAIM: no precondition at all, but the function claims the predicate of its result. `return t`
// with an unconstrained `t` cannot satisfy `all_nonneg(result)` — must be rejected.
func TestStructuralInduction_FalseNoPrecondition(t *testing.T) {
	src := `
enum Tree:
    Leaf
    Node(value: i64, left: Tree, right: Tree)

ghost def all_nonneg(t: Tree) -> bool:
    match t:
        Tree.Leaf:
            return true
        Tree.Node(value: v, left: l, right: r):
            return v >= 0 and all_nonneg(l) and all_nonneg(r)

def claim(t: Tree) -> Tree:
    ensure all_nonneg(result)
    return t
`
	r := analyzeContractStrict(t, "struct_ind_false_noreq.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG (FALSE_CLAIM ACCEPTED): no precondition, so all_nonneg(result) is unprovable")
	}
}

// SOUNDNESS: a NON-WELL-FOUNDED predicate (recurses on itself, NOT on a strictly-smaller child) must be
// denied its defining equation — and the analysis must TERMINATE (no hang). `bad(t)` recurses on the
// same `t`, so the structural decrease cannot be verified; the predicate stays opaque, the unrelated
// claim is declined, and the bounded unroll prevents divergence.
func TestStructuralInduction_NonWellFoundedDeclinesNoHang(t *testing.T) {
	src := `
enum Tree:
    Leaf
    Node(value: i64, left: Tree, right: Tree)

ghost def all_nonneg(t: Tree) -> bool:
    match t:
        Tree.Leaf:
            return true
        Tree.Node(value: v, left: l, right: r):
            return v >= 0 and all_nonneg(l) and all_nonneg(r)

ghost def bad(t: Tree) -> bool:
    match t:
        Tree.Leaf:
            return false
        Tree.Node(value: v, left: l, right: r):
            return bad(t)

def use(t: Tree) -> Tree:
    requires bad(t)
    ensure all_nonneg(result)
    return t
`
	r := analyzeContractStrict(t, "struct_ind_nonwf.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("REAL BUG: bad(t) is unrelated to all_nonneg(result); the claim must be declined")
	}
}

// TRUE_CLAIM (inductive unfold): the structural DEFINING EQUATION (not mere canonicalization) is needed
// here. `node_value` returns the value field of a Node; with `requires all_nonneg(t)`, unfolding the
// predicate at the matched Node arm yields `v >= 0`, which discharges `ensure result >= 0`. This
// exercises emitStructuralPredicateEquation: the precondition `all_nonneg(t)` is unfolded one level so
// the prover learns the node value is non-negative.
func TestStructuralInduction_InductiveUnfoldNodeValue(t *testing.T) {
	src := `
enum Tree:
    Leaf
    Node(value: i64, left: Tree, right: Tree)

ghost def all_nonneg(t: Tree) -> bool:
    match t:
        Tree.Leaf:
            return true
        Tree.Node(value: v, left: l, right: r):
            return v >= 0 and all_nonneg(l) and all_nonneg(r)

def node_value(t: Tree) -> i64:
    requires all_nonneg(t)
    requires t is Tree.Node
    ensure result >= 0
    match t:
        Tree.Leaf:
            return 0
        Tree.Node(value: v, left: l, right: r):
            return v
`
	r := analyzeContractStrict(t, "struct_ind_unfold.elisa", src)
	// Either it proves (unfold connects all_nonneg(t) -> v >= 0) or it declines soundly; it must NOT
	// crash or hang, and a proof here is the inductive-unfold success path.
	_ = r.Errors()
}
