package semantic

import (
	"strings"
	"testing"
)

// Phase 5: a `can[any]` grant (the top permission ⊤) satisfies any concrete
// requirement — the explicit erasure escape.
func TestAnyGrantSatisfiesConcreteRequirement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "any_grant.elisa", `
permission Disk:
    Read

extern read_disk() -> i64 can[Disk.Read]

def build() -> i64:
    can any:
        return read_disk()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `read_disk`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected `can any:` to satisfy a Disk.Read call, got:\n%s", all)
	}
}

// A `can[any]` *requirement* is NOT satisfied by a concrete grant — only `any`
// (or `trusted`) discharges it.
func TestConcreteGrantDoesNotSatisfyAnyRequirement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "any_req.elisa", `
permission Disk:
    Read

extern wild() -> i64 can[any]

def build() -> i64:
    can Disk.Read:
        return wild()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `can[any]`) && !strings.Contains(all, `wild`) {
		t.Fatalf("expected a concrete `can Disk.Read:` grant to NOT satisfy a can[any] requirement, got:\n%s", all)
	}
}

// `any` satisfies a `can[any]` requirement.
func TestAnyGrantSatisfiesAnyRequirement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "any_any.elisa", `
extern wild() -> i64 can[any]

def build() -> i64:
    can any:
        return wild()
`)
	all := allDiagnostics(result)
	if strings.Contains(all, `wild`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected `can any:` to satisfy a can[any] requirement, got:\n%s", all)
	}
}

// `any` is reserved and cannot be declared as a permission family.
func TestAnyPermissionCannotBeDeclared(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "any_redef.elisa", `
permission any:
    Read
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `reserved top permission`) {
		t.Fatalf("expected redefining `any` to be rejected, got:\n%s", all)
	}
}

// `any` does not support member access.
func TestAnyPermissionRejectsMember(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "any_member.elisa", `
extern wild() -> i64 can[any.Read]

def build() -> i64:
    return wild()
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, `does not support member access`) {
		t.Fatalf("expected `any.Read` to be rejected, got:\n%s", all)
	}
}
