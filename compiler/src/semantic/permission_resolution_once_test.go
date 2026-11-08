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
