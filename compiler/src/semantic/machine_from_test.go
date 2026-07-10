package semantic

import (
	"strings"
	"testing"
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

// R4 — a state unreachable from the start is a compile error.
func TestMachineFromUnreachableStateErrors(t *testing.T) {
	all := machineFromDiag(t, "r4.elisa", `
def f(x: i64) -> i64:
    return machine from S.A:
        S.A:
            done 1
        S.B:
            done 2
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
`)
	if !strings.Contains(all, "transition cycle") || !strings.Contains(all, "decreases") {
		t.Fatalf("expected R3 cycle-needs-decreases error, got:\n%s", all)
	}
}

// A cycle WITH a `decreases` measure is accepted (discharge is deferred to the prover).
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
`)
	if strings.Contains(all, "transition cycle") {
		t.Fatalf("cycle with decreases must be accepted, got:\n%s", all)
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
