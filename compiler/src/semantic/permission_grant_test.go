package semantic

import (
	"strings"
	"testing"
)

func TestDeclaredCallPermissionInsideLocalGrantRequiresFullGrantSet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_local_grant.llcontext", `
extern alloc_value() -> i64 can[Abort.Panic, Memory.Allocate]

def build() -> i64 can[Abort.Panic, Memory.Allocate]:
    can Memory.Allocate:
        return alloc_value()
`)
	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, `call to "alloc_value" requires can[Abort] and has no explicit local effect grant; add can Abort.Panic or a surrounding can ...: block`) {
		t.Fatalf("expected missing local Abort grant warning on call, got:\n%s", all)
	}
}

func TestDeclaredCallPermissionWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_with_local_grant.llcontext", `
extern alloc_value() -> i64 can[Memory.Allocate]

def build() -> i64 can[Memory.Allocate]:
    can Memory.Allocate:
        return alloc_value()
`)
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, `alloc_value`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected no local grant warning, got:\n%s", all)
	}
}

func TestDeclaredPanicPermissionStillRequiresLocalGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_panic_permission_local_grant.llcontext", `
def build() -> void can[Abort.Panic, Memory.Allocate]:
    can Memory.Allocate:
        panic("boom")
`)
	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, `panic requires can[Abort] and has no explicit local effect grant; add can Abort.Panic or a surrounding can ...: block`) {
		t.Fatalf("expected missing local Abort grant warning, got:\n%s", all)
	}
}

func TestDeclaredPanicPermissionWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_panic_permission_with_local_grant.llcontext", `
def build() -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        panic("boom")
`)
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, `panic requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected no panic local grant warning, got:\n%s", all)
	}
}
