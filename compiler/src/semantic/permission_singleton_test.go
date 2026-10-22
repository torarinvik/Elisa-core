package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestCanonicalPermissionSingletonOwnsRow(t *testing.T) {
	for _, member := range []string{"", "Allocate"} {
		input := []ast.PermissionRef{{Name: "Memory", Member: member}}
		got := canonicalizePermissionRefs(input)
		if !reflect.DeepEqual(got, input) {
			t.Fatal("singleton metadata changed")
		}
		got[0].Name = "Other"
		if input[0].Name != "Memory" {
			t.Fatal("result aliases caller row")
		}
	}
}

var canonicalPermissionSingletonSink []ast.PermissionRef

func BenchmarkCanonicalPermissionSingleton(b *testing.B) {
	for _, member := range []string{"", "Allocate"} {
		b.Run("member="+member, func(b *testing.B) {
			input := []ast.PermissionRef{{Name: "Memory", Member: member}}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				canonicalPermissionSingletonSink = canonicalizePermissionRefs(input)
			}
		})
	}
}
