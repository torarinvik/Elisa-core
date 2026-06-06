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
