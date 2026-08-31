package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/125 §5 — `machine from` state-graph refusals. States are variants of an existing
// enum; the analyzer validates the graph before lowering.

const machineFromStateEnum = `const enum S of u8:
    A
    B
    C
`

func machineFromDiag(t *testing.T, name, src string) string {
	t.Helper()
	return allDiagnostics(analyzeTreeTestSourceWithSemanticErrors(t, name, machineFromStateEnum+src))
}

func TestStaticWalkerVisitsIndexAndRecoveryChildren(t *testing.T) {
	position := lexer.Pos{}
	seen := func(want string) func(ast.Expr) bool {
		return func(expr ast.Expr) bool {
			call, ok := expr.(*ast.CallExpr)
			return ok && call.Func.(*ast.Ident).Name == want
		}
	}
	analyzer := &Analyzer{}
	indexed := &ast.IndexExpr{
		Position: position,
		Object:   &ast.IntLit{Position: position, Value: "0"},
		Index:    &ast.IntLit{Position: position, Value: "0"},
		Index2: &ast.CallExpr{
			Position: position,
			Func:     &ast.Ident{Position: position, Name: "index_call"},
		},
	}
	if !analyzer.walkStaticExpr(indexed, seen("index_call")) {
		t.Fatal("static walker did not visit the second index operand")
	}
	recovery := &ast.GetExpr{
		Position: position,
		Value:    &ast.IntLit{Position: position, Value: "0"},
		Recovery: &ast.RecoveryClause{Body: []ast.Stmt{&ast.ExprStmt{
			Position: position,
			Expr: &ast.CallExpr{
				Position: position,
				Func:     &ast.Ident{Position: position, Name: "recovery_call"},
			},
		}}},
	}
	if !analyzer.walkStaticExpr(recovery, seen("recovery_call")) {
		t.Fatal("static walker did not visit the recovery body")
	}
}

// R2 — an arm that never transitions is a compile error ("I forgot to decide").
func TestMachineFromArmNoDecisionErrors(t *testing.T) {
	all := machineFromDiag(t, "r2_none.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A:
            done 1
        S.B:
            x + 1
`)
	if !strings.Contains(all, "makes no decision") {
		t.Fatalf("expected R2 no-decision error, got:\n%s", all)
	}
}

// R2 — an arm whose final terminator is guarded can fall through without resolving.
func TestMachineFromGuardedFinalTerminatorErrors(t *testing.T) {
	all := machineFromDiag(t, "r2_guarded.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A:
            done 1 if x > 0
`)
	if !strings.Contains(all, "can complete without a transition") {
		t.Fatalf("expected R2 guarded-final error, got:\n%s", all)
	}
}

// R2 — an earlier unguarded terminator makes every later terminator unreachable. The
// lowering must not silently discard the later branch.
func TestMachineFromEarlierUnguardedTerminatorErrors(t *testing.T) {
	all := machineFromDiag(t, "r2_earlier_unguarded.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A:
            next S.B
            done 0
        S.B:
            done 1
`)
	if !strings.Contains(all, "later terminators are unreachable") {
		t.Fatalf("expected earlier-unguarded terminator error, got:\n%s", all)
	}
}

// R4 — a state unreachable from the start is a compile error.
func TestMachineFromUnreachableStateErrors(t *testing.T) {
	all := machineFromDiag(t, "r4.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A:
            done 1
        S.B:
            done 2
        S.C:
            done 3
`)
	if !strings.Contains(all, "unreachable from the start") {
		t.Fatalf("expected R4 dead-state error, got:\n%s", all)
	}
}

// R3 — a transition cycle with no `decreases` measure is a compile error.
func TestMachineFromCycleWithoutDecreasesErrors(t *testing.T) {
	all := machineFromDiag(t, "r3.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A:
            next S.B if x > 0
            done 0
        S.B:
            next S.A
        S.C:
            done 2
`)
	if !strings.Contains(all, "transition cycle") || !strings.Contains(all, "decreases") {
		t.Fatalf("expected R3 cycle-needs-decreases error, got:\n%s", all)
	}
}

// A cycle WITH a `decreases` measure is accepted; the measure is now discharged by the
// existing loop-termination prover (checkLoopTermination) — here it falls back to the
// runtime progress backstop (advisory), so acceptance holds.
func TestMachineFromCycleWithDecreasesOk(t *testing.T) {
	all := machineFromDiag(t, "r3_ok.elisa", `
def f(x: i64) -> i64:
    n: mutable i64 = x
    return machine from S.A decreases n:
        S.A:
            n <- n - 1
            next S.B if n > 0
            done n
        S.B:
            next S.A
        S.C:
            done 2
`)
	if strings.Contains(all, "transition cycle") {
		t.Fatalf("cycle with decreases must be accepted, got:\n%s", all)
	}
}

// Compound assignment is a straight-line mutation just like `<-`. Stage0 represents
// these with dedicated AST nodes, while stage1 keeps the assignment operator on Assign;
// both frontends must accept the same machine-from arm and capture its outer mutable.
func TestMachineFromCompoundAssignmentOk(t *testing.T) {
	all := machineFromDiag(t, "compound_assign.elisa", `
def f() -> i64:
    n: mutable i64 = 0
    return machine from S.A:
        S.A:
            n += 1
            done n
        S.B:
            done 0
        S.C:
            done 0
`)
	if strings.Contains(all, "machine arms allow only straight-line") {
		t.Fatalf("compound assignment in a straight-line machine arm must be accepted, got:\n%s", all)
	}
}

// An arm-local temporary is introduced inside the generated match arm. It must remain
// local to that arm; capturing it as if it were an outer mutable produces a spurious E11.
func TestMachineFromArmLocalAssignmentIsNotCaptured(t *testing.T) {
	all := machineFromDiag(t, "local_arm_value.elisa", `
def f() -> i64:
    return machine from S.A:
        S.A:
            tmp: mutable i64 = 0
            tmp <- tmp + 1
            done tmp
        S.B:
            done 0
        S.C:
            done 0
`)
	if strings.Contains(all, "capture \"tmp\"") {
		t.Fatalf("arm-local temporary must not be captured by machine-from lowering, got:\n%s", all)
	}
}

// A mutable-reference call is a machine arm mutation even though its target is not an
// assignment node. The lowering must discover that effect from the callee signature so E4
// does not reject a valid machine arm as a hidden outer write.
func TestMachineFromMutableCallCapturesOuterRoot(t *testing.T) {
	all := machineFromDiag(t, "mutable_call.elisa", `
def bump(value: mutable i64&) -> void:
    value <- value + 1

def f() -> i64:
    n: mutable i64 = 0
    return machine from S.A:
        S.A:
            bump(n)
            done n
        S.B:
            done 0
        S.C:
            done 0
`)
	if strings.Contains(all, "value block may not mutate the outer binding \"n\"") {
		t.Fatalf("mutable call root must be captured by machine-from lowering, got:\n%s", all)
	}
}

// Builtin collection methods are value-receiver calls and therefore have no mutable-ref
// parameter for the capture pass to inspect. The receiver type must still be recovered from
// the already-collected binding before the generated value block is analyzed.
func TestMachineFromBuiltinCallCapturesOuterRoot(t *testing.T) {
	all := machineFromDiag(t, "builtin_mutation.elisa", `
def f() -> usize:
	items: mutable darray[i64] = []
	return machine from S.A:
		S.A:
			items.push(1)
			items.pop()
			done items.count
        S.B:
            done 0
        S.C:
            done 0
`)
	if strings.Contains(all, `value block may not mutate the outer binding "items"`) {
		t.Fatalf("builtin receiver must be captured by machine-from lowering, got:\n%s", all)
	}
}

// A header capture can also be rediscovered by the mutation pass (for example, when the
// same darray is explicitly threaded and receives a builtin mutation). The lowered capture
// manifest is a set and must not contain duplicate entries.
func TestMachineFromCaptureManifestDeduplicatesHeaderAndMutation(t *testing.T) {
	position := lexer.Pos{}
	name := &ast.Ident{Position: position, Name: "items"}
	expr := &ast.MachineFromExpr{
		Position:       position,
		StartEnum:      "S",
		StartState:     "A",
		HeaderCaptures: []string{"items", "items"},
		Arms: []ast.MachineFromArm{{
			Position: position,
			State:    "A",
			Body: []ast.Stmt{&ast.AssignStmt{
				Position: position,
				Target:   name,
				Value:    &ast.IntLit{Position: position, Value: "1"},
			}},
		}},
	}
	lowered, ok := (&Analyzer{}).buildMachineFromLowering(expr, 1, builtinI64Type()).(*ast.ExprBlock)
	if !ok {
		t.Fatalf("machine-from lowering returned %T, want *ast.ExprBlock", expr.Lowered)
	}
	if len(lowered.Captures) != 1 || lowered.Captures[0] != "items" {
		t.Fatalf("lowered capture manifest = %v, want [items]", lowered.Captures)
	}
}

// Every variant in the existing state enum must have a handler. A generated wildcard cannot
// safely turn an omitted reachable state into a zero-valued result.
func TestMachineFromMissingArmErrors(t *testing.T) {
	all := machineFromDiag(t, "missing_arm.elisa", `
def f() -> i64:
    return machine from S.A:
        S.A:
            done 1
        S.B:
            done 2
`)
	if !strings.Contains(all, "has no arm") {
		t.Fatalf("expected missing-state-arm error, got:\n%s", all)
	}
}

// The `decreases` measure now flows into the loop-termination prover, so a non-integer
// measure is a hard error — proving the measure is genuinely type-checked, not merely
// present-checked (the pre-wiring behavior silently ignored the measure's type).
func TestMachineFromDecreasesMeasureMustBeInteger(t *testing.T) {
	all := machineFromDiag(t, "dec_type.elisa", `
def f(flag: bool) -> i64:
    return machine from S.A decreases flag:
        S.A:
            next S.B if flag
            done 0
        S.B:
            next S.A if flag
            next S.C
        S.C:
            done 2
`)
	if !strings.Contains(all, "measure must be an integer") {
		t.Fatalf("expected non-integer decreases measure to be rejected, got:\n%s", all)
	}
}

// R5 — an arm that declares its out-edges (`-> {…}`) may only take a listed transition.
func TestMachineFromDeclaredOutViolationErrors(t *testing.T) {
	all := machineFromDiag(t, "r5_violation.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A -> {B}:
            next S.B if x > 0
            next S.C
        S.B:
            done 1
        S.C:
            done 2
`)
	if !strings.Contains(all, "declared out-edges") || !strings.Contains(all, "next C") {
		t.Fatalf("expected R5 undeclared-transition error, got:\n%s", all)
	}
}

// R5 — a declared out-edge that is not a variant of the state enum is a compile error.
func TestMachineFromDeclaredOutNonVariantErrors(t *testing.T) {
	all := machineFromDiag(t, "r5_nonvariant.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A -> {Zzz}:
            next S.B
        S.B:
            done 1
`)
	if !strings.Contains(all, "out-edge") || !strings.Contains(all, "not a variant") {
		t.Fatalf("expected R5 non-variant out-edge error, got:\n%s", all)
	}
}

// R5 — an arm whose actual transitions exactly match its declared set is accepted.
func TestMachineFromDeclaredOutHonoredOk(t *testing.T) {
	all := machineFromDiag(t, "r5_ok.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A -> {B, C}:
            next S.B if x > 0
            next S.C
        S.B:
            done 1
        S.C:
            done 2
`)
	if strings.Contains(all, "declared out-edges") || strings.Contains(all, "out-edge") {
		t.Fatalf("declared out-edges honored must be accepted, got:\n%s", all)
	}
}

// An arm state that is not a variant of the start enum is a compile error.
func TestMachineFromUnknownStateErrors(t *testing.T) {
	all := machineFromDiag(t, "unknown.elisa", `
def f() -> i64:
    return machine from S.A:
        S.A:
            done 1
        S.Zzz:
            done 2
`)
	if !strings.Contains(all, "not a variant") {
		t.Fatalf("expected unknown-state error, got:\n%s", all)
	}
}
