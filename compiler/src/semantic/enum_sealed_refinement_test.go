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

// docs/77 Phase 2 groundwork: a hierarchy root gets a dense, hierarchy-grouped leaf-tag space, so
// each category occupies a contiguous NESTED range — the basis for `is X` as a range check and a
// zero-cost upcast.
func TestEnumHierarchyTagRangesAreNestedAndContiguous(t *testing.T) {
	result := analyzeTreeTestSource(t, "enum_tags.elisa", `enum Expression: pass
enum BinaryExpression is Expression:
    Add(left: i64, right: i64)
    Mul(left: i64, right: i64)
enum Literal is Expression:
    Int(value: i64)
`)
	expr := result.NamedTypes["Expression"].(*EnumType)
	bin := result.NamedTypes["BinaryExpression"].(*EnumType)
	lit := result.NamedTypes["Literal"].(*EnumType)

	// Root spans all three leaves; children partition it contiguously, in declaration order.
	if expr.LeafTagLo != 0 || expr.LeafTagCount != 3 {
		t.Fatalf("Expression range = [%d,+%d), want [0,+3)", expr.LeafTagLo, expr.LeafTagCount)
	}
	if bin.LeafTagLo != 0 || bin.LeafTagCount != 2 {
		t.Fatalf("BinaryExpression range = [%d,+%d), want [0,+2)", bin.LeafTagLo, bin.LeafTagCount)
	}
	if lit.LeafTagLo != 2 || lit.LeafTagCount != 1 {
		t.Fatalf("Literal range = [%d,+%d), want [2,+1)", lit.LeafTagLo, lit.LeafTagCount)
	}
	// Child range nests inside the parent's.
	if !(bin.LeafTagLo >= expr.LeafTagLo && bin.LeafTagLo+bin.LeafTagCount <= expr.LeafTagLo+expr.LeafTagCount) {
		t.Fatalf("BinaryExpression range must nest within Expression's")
	}
	// Leaf variant tags are unified across the hierarchy (0,1 for binops; 2 for the literal).
	add, _ := bin.Variant("Add")
	mul, _ := bin.Variant("Mul")
	intLit, _ := lit.Variant("Int")
	if add.Tag != 0 || mul.Tag != 1 || intLit.Tag != 2 {
		t.Fatalf("unified tags wrong: Add=%d Mul=%d Int=%d, want 0,1,2", add.Tag, mul.Tag, intLit.Tag)
	}
}

// docs/77: inside a match arm naming a refinement, the scrutinee is narrowed to that sub-category,
// so it can be passed where the narrower type is required.
func TestEnumHierarchyMatchNarrowsScrutinee(t *testing.T) {
	analyzeTreeTestSource(t, "enum_match_narrow.elisa", enumHierarchyMatchPrelude+`
def fold_binop(b: BinaryExpression) -> i64:
    return 0

def visit(e: Expression) -> i64:
    out: mutable i64 = 0
    match e:
        BinaryExpression.Add(left: l, right: r):
            out <- fold_binop(e)
        BinaryExpression.Mul(left: l, right: r):
            out <- fold_binop(e)
        Literal.Int(value: v):
            out <- v
    return out
`)
}

// The narrowing must NOT leak: a Literal arm cannot pass the scrutinee as a BinaryExpression.
func TestEnumHierarchyMatchNarrowingIsArmScoped(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "enum_match_narrow_bad.elisa", enumHierarchyMatchPrelude+`
def fold_binop(b: BinaryExpression) -> i64:
    return 0

def visit(e: Expression) -> i64:
    out: mutable i64 = 0
    match e:
        Literal.Int(value: v):
            out <- fold_binop(e)
        BinaryExpression.Add(left: l, right: r):
            out <- 0
        BinaryExpression.Mul(left: l, right: r):
            out <- 0
    return out
`)
	if len(result.Errors()) == 0 {
		t.Fatalf("expected error: in a Literal arm, the scrutinee is not a BinaryExpression")
	}
}

// docs/77: an `is` variant test in expression position accepts a refinement's variant
// (`e is BinaryExpression.Add`) when matching a value of the parent type.
func TestEnumHierarchyIsExprAcceptsRefinementVariant(t *testing.T) {
	analyzeTreeTestSource(t, "enum_is_expr.elisa", enumHierarchyMatchPrelude+`
def is_add(e: Expression) -> bool:
    return e is BinaryExpression.Add

def reject_alien(e: Expression) -> bool:
    return e is Literal.Int
`)
}

func TestEnumHierarchyIsExprRejectsUnrelatedEnum(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "enum_is_expr_bad.elisa", enumHierarchyMatchPrelude+`
enum Other: pass
enum Outsider is Other:
    Nope(x: i64)

def bad(e: Expression) -> bool:
    return e is Outsider.Nope
`)
	if len(result.Errors()) == 0 {
		t.Fatalf("expected error: Outsider.Nope is not a variant reachable from Expression")
	}
}

// docs/77: a bare-category `is` test `e is BinaryExpression` (no `.Variant`) type-checks on a
// hierarchy scrutinee, and rejects an unrelated enum.
func TestEnumHierarchyBareCategoryIsTest(t *testing.T) {
	analyzeTreeTestSource(t, "enum_is_cat.elisa", enumHierarchyMatchPrelude+`
def is_binop(e: Expression) -> bool:
    return e is BinaryExpression

def is_literal(e: Expression) -> bool:
    return e is Literal
`)
}

func TestEnumHierarchyBareCategoryIsRejectsUnrelated(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "enum_is_cat_bad.elisa", enumHierarchyMatchPrelude+`
enum Other: pass
enum Outsider is Other:
    Nope(x: i64)

def bad(e: Expression) -> bool:
    return e is Outsider
`)
	if len(result.Errors()) == 0 {
		t.Fatalf("expected error: Outsider is not in Expression's hierarchy")
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
