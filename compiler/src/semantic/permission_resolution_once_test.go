package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestPermissionFamiliesUseResolvedReferences(t *testing.T) {
	refs := []ast.PermissionRef{{Name: permissionAnyName}}
	a := &Analyzer{}
	resolved := a.resolvePermissionRefs(refs, true)
	if got, want := permissionFamiliesFromRefs(resolved), a.resolvePermissionFamilies(refs, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("families differ: %v vs %v", got, want)
	}
}

func TestResolvedSingletonPermissionOwnsRow(t *testing.T) {
	refs := []ast.PermissionRef{{Name: permissionAnyName}}
	resolved := (&Analyzer{}).resolvePermissionRefs(refs, true)
	if len(resolved) != 1 {
		t.Fatal("singleton permission missing")
	}
	resolved[0].Name = "changed"
	if refs[0].Name != permissionAnyName {
		t.Fatal("resolved row aliases source")
	}
}

func TestRecordSingletonPermissionOwnsRow(t *testing.T) {
	refs := []ast.PermissionRef{{Name: "Memory", Member: "Read"}}
	a := &Analyzer{analyzerControlFlowState: analyzerControlFlowState{
		currentFunctionUsedPermissions: map[string]bool{},
	}}
	a.recordFunctionPermissionRefs(refs)
	refs[0].Member = "Write"
	if len(a.currentFunctionUsedPermissionRefs) != 1 || a.currentFunctionUsedPermissionRefs[0].Member != "Read" || !a.currentFunctionUsedPermissions["Memory"] {
		t.Fatal("recorded permission lost its value or family")
	}
}

func BenchmarkPermissionResolutionForSignature(b *testing.B) {
	refs := []ast.PermissionRef{{Name: permissionAnyName}}
	a := &Analyzer{}
	for _, repeated := range []bool{true, false} {
		name := "single"
		if repeated {
			name = "repeated"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				resolved := a.resolvePermissionRefs(refs, true)
				if repeated {
					_ = a.resolvePermissionFamilies(refs, true)
				} else {
					_ = permissionFamiliesFromRefs(resolved)
				}
			}
		})
	}
}

func BenchmarkRecordSingletonPermission(b *testing.B) {
	a := &Analyzer{analyzerControlFlowState: analyzerControlFlowState{
		currentFunctionUsedPermissions: map[string]bool{},
	}}
	refs := []ast.PermissionRef{{Name: "Memory", Member: "Read"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.currentFunctionUsedPermissionRefs = a.currentFunctionUsedPermissionRefs[:0]
		a.recordFunctionPermissionRefs(refs)
	}
}
