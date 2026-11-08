package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

var permissionPairBenchSink []ast.PermissionRef

func BenchmarkCanonicalPermissionPair(b *testing.B) {
	refs := []ast.PermissionRef{{Name: "Global", Member: "Write"}, {Name: "Global", Member: "Read"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		permissionPairBenchSink = canonicalizePermissionRefs(refs)
	}
}
func TestCanonicalPermissionPairMatchesGeneralPath(t *testing.T) {
	for _, members := range [][2]string{{"Read", "Write"}, {"Write", "Read"}, {"a", "z"}} {
		left := ast.PermissionRef{Name: "Global", Member: members[0], Position: lexer.Pos{Line: 1}}
		right := ast.PermissionRef{Name: "Global", Member: members[1], Position: lexer.Pos{Line: 2}}
		got := canonicalizePermissionRefs([]ast.PermissionRef{left, right})
		want := canonicalizePermissionRefs([]ast.PermissionRef{left, right, {}})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
}

var permissionFamiliesBenchSink []string

func BenchmarkPermissionFamiliesPair(b *testing.B) {
	refs := []ast.PermissionRef{{Name: "Global", Member: "Write"}, {Name: "Global", Member: "Read"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		permissionFamiliesBenchSink = permissionFamiliesFromRefs(refs)
	}
}
func TestPermissionFamiliesPairMatchesGeneral(t *testing.T) {
	for _, name := range []string{"Global", "A.B"} {
		refs := []ast.PermissionRef{{Name: name, Member: "Read"}, {Name: name, Member: "Write"}}
		got := permissionFamiliesFromRefs(refs)
		want := permissionFamiliesFromRefs(append(append([]ast.PermissionRef(nil), refs...), ast.PermissionRef{}))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
