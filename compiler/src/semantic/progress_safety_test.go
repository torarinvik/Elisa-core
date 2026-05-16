package semantic

import (
	"strings"
	"testing"
)

func TestProgressSafetyWarnsForUnbudgetedWhileLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_unbudgeted_while.elisa", `
def spin(flag: bool) -> void:
    while flag:
        pass
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, "progress warning: while loop has no progress evidence") {
		t.Fatalf("expected unbudgeted while-loop progress warning, got:\n%s", all)
	}
}

func TestProgressSafetyAllowsLoopWithProgressTickEvidence(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_tick_while.elisa", `
def spin(flag: bool) -> void:
    while flag:
        can Progress.Tick:
            signal Progress.Tick
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "progress warning") {
		t.Fatalf("expected Progress.Tick to discharge loop progress obligation, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("spin")
	if !ok {
		t.Fatal("expected spin symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected spin function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Progress.Tick]" {
		t.Fatalf("expected visible Progress.Tick permission, got %q", got)
	}
}

func TestProgressSafetyRequiresLoopLocalEvidence(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_tick_outside_while.elisa", `
def spin(flag: bool) -> void:
    can Progress.Tick:
        signal Progress.Tick
    while flag:
        pass
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, "progress warning: while loop has no progress evidence") {
		t.Fatalf("expected progress evidence outside loop not to discharge loop obligation, got:\n%s", all)
	}
}

func TestProgressSafetyAllowsTrustedIntentionalNonProgressLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_trusted_nonprogress_while.elisa", `
def spin(flag: bool) -> void:
    trusted Unsafe.NonProgress:
        while flag:
            pass
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "progress warning") {
		t.Fatalf("expected trusted Unsafe.NonProgress to discharge loop locally, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("spin")
	if !ok {
		t.Fatal("expected spin symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected spin function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted non-progress implementation detail not to infer caller permissions, got %q", got)
	}
}

func TestProgressSafetyWarnsForInfiniteRecursionPressureCase(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_recursive_cycle.elisa", `
def ping() -> void:
    pong()

def pong() -> void:
    ping()
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, "progress warning: recursive cycle") {
		t.Fatalf("expected recursive-cycle progress warning, got:\n%s", all)
	}
}

func TestProgressSafetyAllowsRecursiveCycleWithProgressEvidence(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "progress_recursive_cycle_budgeted.elisa", `
def ping() -> void:
    can Progress.EnterRecursion:
        signal Progress.EnterRecursion
    pong()

def pong() -> void:
    ping()
`, AnalyzeOptions{EnforceProgressSafety: true})

	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "progress warning") {
		t.Fatalf("expected Progress.EnterRecursion to discharge recursive-cycle progress obligation, got:\n%s", all)
	}
}
