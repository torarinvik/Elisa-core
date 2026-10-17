//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

func instantiatedEffect(typ, via string) ast.PermissionRef {
	ref := ast.PermissionRef{Name: "Writer", TypeArgs: []ast.TypeExpr{&ast.NamedType{Name: typ}}}
	if via != "" {
		ref.Via = []ast.PermissionRef{{Name: via, Member: "Write"}}
	}
	return ref
}

func TestPermissionInstantiationIdentity(t *testing.T) {
	original := instantiatedEffect("i64", "Console")
	for _, other := range []ast.PermissionRef{instantiatedEffect("sview", "Console"), instantiatedEffect("i64", "File"), instantiatedEffect("i64", "")} {
		if samePermissionRefs([]ast.PermissionRef{original}, []ast.PermissionRef{other}) {
			t.Fatalf("different specialization/realization compared equal: %#v", other)
		}
		for _, pair := range [][]ast.PermissionRef{{original, other}, {other, original}} {
			got := canonicalizePermissionRefs(pair)
			if len(got) != 2 {
				t.Fatalf("distinct effects collapsed: %#v", got)
			}
			if !samePermissionRefs(got, pair) {
				t.Fatal("canonical row did not preserve identity")
			}
		}
	}
	moved := instantiatedEffect("i64", "Console")
	moved.Position = lexer.Pos{File: "new.elisa", Line: 500, Col: 3}
	moved.TypeArgs[0].(*ast.NamedType).Position = moved.Position
	moved.Via[0].Position = moved.Position
	if !samePermissionRefs([]ast.PermissionRef{original}, []ast.PermissionRef{moved}) {
		t.Fatal("source positions changed effect identity")
	}
	if got := canonicalizePermissionRefs([]ast.PermissionRef{original, moved}); len(got) != 1 {
		t.Fatalf("equivalent effects not merged: %#v", got)
	}
}

func TestPermissionInstantiationFamilyCoverage(t *testing.T) {
	family := instantiatedEffect("i64", "Console")
	sameMember := instantiatedEffect("i64", "Console")
	sameMember.Member = "Write"
	otherMember := instantiatedEffect("sview", "Console")
	otherMember.Member = "Write"
	plainMember := ast.PermissionRef{Name: "Writer", Member: "Write"}
	input := []ast.PermissionRef{sameMember, otherMember, family, plainMember}
	got := canonicalizePermissionRefs(input)
	if len(got) != 3 {
		t.Fatalf("family erased a different instantiation: %#v", got)
	}
	reversed := []ast.PermissionRef{plainMember, family, otherMember, sameMember}
	if !reflect.DeepEqual(got, canonicalizePermissionRefs(reversed)) {
		t.Fatal("row depends on input order")
	}
	if !permissionRefGranted(otherMember, map[string]bool{"Writer": true}) {
		t.Fatal("family authorization changed")
	}
	if !permissionRefGranted(plainMember, map[string]bool{"Writer.Write": true}) {
		t.Fatal("member authorization changed")
	}
	if permissionRefGranted(plainMember, map[string]bool{"Other": true}) {
		t.Fatal("unrelated family authorized effect")
	}
}

func TestPermissionInstantiationInferenceEditHistory(t *testing.T) {
	f := newPermissionGraphFixture(3)
	f.edge(0, 1)
	f.edge(1, 2)
	f.edge(2, -1)
	for _, row := range [][]ast.PermissionRef{
		{instantiatedEffect("i64", "Console")},
		{instantiatedEffect("sview", "Console")},
		{instantiatedEffect("sview", "File")},
		{instantiatedEffect("i64", "Console"), instantiatedEffect("sview", "File")},
		{instantiatedEffect("i64", "Console")},
	} {
		f.a.functionTypes["native"].PermissionRefs = row
		f.a.inferFunctionPermissionEffectsWorklist(f.decls)
		for i, ft := range f.types {
			if !samePermissionRefs(ft.PermissionRefs, row) {
				t.Fatalf("step %#v function%d stale: %#v", row, i, ft.PermissionRefs)
			}
		}
	}
}

func TestPermissionInstantiationSurvivesSourceAnalysis(t *testing.T) {
	source := `effect Writer[T]:
    def write(value: T) -> void

permission TestSink:
    Write

extern integer_writer() can[Writer[i64] via TestSink.Write]
extern text_writer() can[Writer[sview] via TestSink.Write]

def use_both():
    integer_writer()
    text_writer()
`
	result := Analyze(parseIncrementalSessionFile(t, source))
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	ft := result.GlobalScope.Symbols["use_both"].Type.(*FuncType)
	count := 0
	for _, ref := range ft.PermissionRefs {
		if ref.Name == "Writer" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("typed effects lost in declared/inferred rows: %#v", ft.PermissionRefs)
	}
}

func TestPermissionInstantiationUnencodableHasBoundedLocalUnion(t *testing.T) {
	var typ ast.TypeExpr = &ast.NamedType{Name: "i64"}
	for i := 0; i < functionSyntaxMaxDepth; i++ {
		typ = &ast.GenericType{Name: "Box", Args: []ast.TypeExpr{typ}}
	}
	ref := ast.PermissionRef{Name: "Writer", TypeArgs: []ast.TypeExpr{typ}}
	if _, ok := permissionInstantiation(ref); ok {
		t.Fatal("oversized syntax admitted as durable identity")
	}
	if !samePermissionRefs([]ast.PermissionRef{ref}, []ast.PermissionRef{ref}) {
		t.Fatal("exact local comparison failed")
	}
	if got := canonicalizePermissionRefs([]ast.PermissionRef{ref, ref}); len(got) != 1 {
		t.Fatal("oversized identity duplicated")
	}
}

func TestPermissionInstantiationOversizedRecursiveRowsStayBounded(t *testing.T) {
	var typ ast.TypeExpr = &ast.NamedType{Name: "i64"}
	for i := 0; i < functionSyntaxMaxDepth; i++ {
		typ = &ast.GenericType{Name: "Box", Args: []ast.TypeExpr{typ}}
	}
	ref := ast.PermissionRef{Name: "Writer", TypeArgs: []ast.TypeExpr{typ}}
	f := newPermissionGraphFixture(4)
	f.edge(0, 1)
	f.edge(0, 2)
	f.edge(1, 3)
	f.edge(2, 3)
	f.edge(3, 0)
	f.edge(3, -1)
	f.a.functionTypes["native"].PermissionRefs = []ast.PermissionRef{ref}
	f.a.inferFunctionPermissionEffectsWorklist(f.decls)
	for _, ft := range f.types {
		if len(ft.PermissionRefs) != 1 {
			t.Fatalf("opaque recursive row grew: %d", len(ft.PermissionRefs))
		}
	}
}

func TestPermissionInstantiationOversizedDuplicateSelfReadsStayBounded(t *testing.T) {
	var typ ast.TypeExpr = &ast.NamedType{Name: "i64"}
	for i := 0; i < functionSyntaxMaxDepth; i++ {
		typ = &ast.GenericType{Name: "Box", Args: []ast.TypeExpr{typ}}
	}
	f := newPermissionGraphFixture(8)
	f.types[0].DeclaredPermissionRefs = []ast.PermissionRef{{Name: "Writer", TypeArgs: []ast.TypeExpr{typ}}}
	f.edge(0, 0)
	f.edge(0, 0)
	f.a.inferFunctionPermissionEffectsWorklist(f.decls)
	if len(f.types[0].PermissionRefs) != 1 {
		t.Fatalf("duplicate recursive reads multiplied oversized effects: %d", len(f.types[0].PermissionRefs))
	}
}
