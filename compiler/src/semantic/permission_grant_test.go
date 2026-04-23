package semantic

import (
	"strings"
	"testing"
)

func TestDeclaredCallPermissionRequiresTopLevelGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_local_grant.llcontext", `
extern alloc_value() -> i64 can[Abort.Panic, Memory.Allocate]

def build() -> i64:
	return alloc_value()
`)
	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, `call to "alloc_value" requires can[Abort, Memory] and has no explicit local effect grant; add  can[Abort.Panic, Memory.Allocate] or a surrounding can ...: block`) {
		t.Fatalf("expected missing top-level grant warning on call, got:\n%s", all)
	}
}

func TestDeclaredCallPermissionCanInheritOuterExplicitGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_inner_local_grant.llcontext", `
extern alloc_value() -> i64 can[Abort.Panic, Memory.Allocate]

def build() -> i64:
    can Abort.Panic, Memory.Allocate:
        can Memory.Allocate:
            return alloc_value()
`)
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, `alloc_value`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected outer explicit grant to satisfy nested call, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic, Memory.Allocate]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
}

func TestDeclaredCallPermissionWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_call_permission_with_local_grant.llcontext", `
extern alloc_value() -> i64 can[Memory.Allocate]

def build() -> i64:
    can Memory.Allocate:
        return alloc_value()
`)
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, `alloc_value`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected no local grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Memory.Allocate]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
}

func TestDeclaredPanicPermissionRequiresTopLevelGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_panic_permission_top_level_grant.llcontext", `
def build() -> void:
    panic("boom")
`)
	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, `panic requires can[Abort] and has no explicit local effect grant; add can Abort.Panic or a surrounding can ...: block`) {
		t.Fatalf("expected missing top-level Abort grant warning, got:\n%s", all)
	}
}

func TestDeclaredPanicPermissionCanInheritOuterExplicitGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_panic_permission_local_grant.llcontext", `
def build() -> void:
    can Abort.Panic, Memory.Allocate:
        can Memory.Allocate:
            panic("boom")
`)
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, `panic requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected outer explicit grant to satisfy nested panic, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic, Memory.Allocate]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
}

func TestDeclaredPanicPermissionWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "declared_panic_permission_with_local_grant.llcontext", `
def build() -> void:
    can Abort.Panic:
        panic("boom")
`)
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, `panic requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected no panic local grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic]" {
		t.Fatalf("expected inferred build permissions, got %q", got)
	}
}
