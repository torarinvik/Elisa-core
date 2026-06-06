package semantic

import "testing"

// docs/77 Phase 1: `enum Child is Parent:` parses and establishes sealed nominal subtyping —
// Child's cases are a subset of Parent's, so Child <: Parent (widening assignable), but not the
// reverse, and siblings are unrelated. Tested at the type level (the unified-tag lowering is a later
// phase, so values are not constructed/run yet).
func TestEnumSealedRefinementSubtype(t *testing.T) {
	result := analyzeTreeTestSource(t, "enum_is.elisa", `enum Shape: pass
enum Round is Shape:
    Circle(radius: i64)
enum Angular is Shape:
    Square(side: i64)
`)
	shape, ok := result.NamedTypes["Shape"].(*EnumType)
	if !ok {
		t.Fatalf("Shape is %T, want *EnumType", result.NamedTypes["Shape"])
	}
	round, ok := result.NamedTypes["Round"].(*EnumType)
	if !ok {
		t.Fatalf("Round is %T, want *EnumType", result.NamedTypes["Round"])
	}
	angular := result.NamedTypes["Angular"].(*EnumType)

	if round.Parent != shape {
		t.Fatalf("Round.Parent = %v, want Shape", round.Parent)
	}
	if shape.Parent != nil {
		t.Fatalf("Shape (root) should have no parent, got %v", shape.Parent)
	}

	// Child <: Parent — widening is assignable.
	if !AssignableTo(shape, round) {
		t.Errorf("Round should be assignable to Shape (Round <: Shape)")
	}
	if !AssignableTo(shape, angular) {
		t.Errorf("Angular should be assignable to Shape (Angular <: Shape)")
	}
	// Parent is NOT assignable to Child — no implicit narrowing.
	if AssignableTo(round, shape) {
		t.Errorf("Shape must NOT be implicitly assignable to Round")
	}
	// Siblings are unrelated.
	if AssignableTo(round, angular) || AssignableTo(angular, round) {
		t.Errorf("sibling categories Round/Angular must not be assignable to each other")
	}
}

// Deep chains: Assignment is Statement is Node ⟹ Assignment <: Statement <: Node, transitively.
func TestEnumSealedRefinementDeepChain(t *testing.T) {
	result := analyzeTreeTestSource(t, "enum_is_deep.elisa", `enum Node: pass
enum Statement is Node:
    Return(value: i64)
enum Assignment is Statement:
    Let(slot: i64, value: i64)
`)
	node := result.NamedTypes["Node"].(*EnumType)
	statement := result.NamedTypes["Statement"].(*EnumType)
	assignment := result.NamedTypes["Assignment"].(*EnumType)

	if assignment.Parent != statement || statement.Parent != node {
		t.Fatalf("parent chain wrong: Assignment.Parent=%v Statement.Parent=%v", assignment.Parent, statement.Parent)
	}
	// Transitive widening: Assignment <: Statement <: Node.
	if !AssignableTo(statement, assignment) {
		t.Errorf("Assignment should be assignable to Statement")
	}
	if !AssignableTo(node, assignment) {
		t.Errorf("Assignment should be assignable to Node (transitive)")
	}
	if !AssignableTo(node, statement) {
		t.Errorf("Statement should be assignable to Node")
	}
	// No upward implicit narrowing anywhere.
	if AssignableTo(assignment, node) || AssignableTo(statement, node) {
		t.Errorf("Node must not be implicitly assignable to a narrower category")
	}
}

// docs/77: a `match` over a hierarchy scrutinee accepts arms naming any refinement's leaf
// (`BinaryExpression.Add` when matching an `Expression`), and is exhaustive over the union of all
// descendant leaves. Analysis-only (no lowering yet).
const enumHierarchyMatchPrelude = `enum Expression: pass
enum BinaryExpression is Expression:
    Add(left: i64, right: i64)
    Mul(left: i64, right: i64)
enum Literal is Expression:
    Int(value: i64)
`

func TestEnumHierarchyMatchExhaustive(t *testing.T) {
	analyzeTreeTestSource(t, "enum_match_ok.elisa", enumHierarchyMatchPrelude+`
def describe(e: Expression) -> i64:
    out: mutable i64 = 0
    match e:
        BinaryExpression.Add(left: l, right: r):
            out <- l + r
        BinaryExpression.Mul(left: l, right: r):
            out <- l * r
        Literal.Int(value: v):
            out <- v
    return out
`)
}

func TestEnumHierarchyMatchExpressionExhaustive(t *testing.T) {
	// A match EXPRESSION requires exhaustiveness — full coverage of the descendant-leaf union is
	// accepted with no wildcard.
	analyzeTreeTestSource(t, "enum_match_expr_ok.elisa", enumHierarchyMatchPrelude+`
def describe(e: Expression) -> i64:
    return match e:
        BinaryExpression.Add(left: l, right: r):
            l + r
        BinaryExpression.Mul(left: l, right: r):
            l * r
        Literal.Int(value: v):
            v
`)
}

func TestEnumHierarchyMatchNonExhaustiveErrors(t *testing.T) {
	// Match EXPRESSION missing BinaryExpression.Mul must be flagged non-exhaustive.
	result := analyzeTreeTestSourceWithSemanticErrors(t, "enum_match_partial.elisa", enumHierarchyMatchPrelude+`
def describe(e: Expression) -> i64:
    return match e:
        BinaryExpression.Add(left: l, right: r):
            l + r
        Literal.Int(value: v):
            v
`)
	if len(result.Errors()) == 0 {
		t.Fatalf("expected a non-exhaustive match error (missing BinaryExpression.Mul)")
	}
}

func TestEnumHierarchyMatchRejectsNonDescendant(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "enum_match_alien.elisa", enumHierarchyMatchPrelude+`
enum Other: pass
enum Outsider is Other:
    Nope(x: i64)

def describe(e: Expression) -> i64:
    out: mutable i64 = 0
    match e:
        Outsider.Nope(x: n):
            out <- n
    return out
`)
	if len(result.Errors()) == 0 {
		t.Fatalf("expected an error: Outsider is not a refinement of Expression")
	}
}

// Refining a non-enum (or unknown) type is a clear error, not a crash.
func TestEnumRefinesNonEnumErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "enum_is_bad.elisa", `struct Plain:
    x: i64
enum Bad is Plain:
    A(value: i64)
`)
	if len(result.Errors()) == 0 {
		t.Fatalf("expected an error for `enum Bad is Plain` (Plain is a struct, not an enum)")
	}
}
