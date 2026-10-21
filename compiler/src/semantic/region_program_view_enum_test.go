package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestProgramLivedViewsInsideOrdinaryEnum(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "program_view_enum.elisa", `global region source
enum Safe:
    Text(sview @source)
enum Unsafe:
    Text(sview)
struct SafeIndex:
    rows: darray[Safe]
struct UnsafeIndex:
    rows: darray[Unsafe]
`, AnalyzeOptions{})
	a := result.analyzer
	for _, test := range []struct {
		name string
		want bool
	}{{"Safe", true}, {"SafeIndex", true}, {"Unsafe", false}, {"UnsafeIndex", false}} {
		value := a.namedTypes[test.name]
		if value == nil {
			t.Fatalf("missing type %s", test.name)
		}
		got := a.typeViewsProgramLived(value, map[Type]bool{})
		if got != test.want {
			t.Errorf("%s program-lived views=%v, want %v", test.name, got, test.want)
		}
	}
}

func TestProgramLivedEnumViewsKeepStoreAndRefinementConstraints(t *testing.T) {
	a := &Analyzer{programRegions: map[string]bool{"source": true}}
	local := &EnumType{Name: "Local", Packed: true}
	if a.typeViewsProgramLived(local, map[Type]bool{}) {
		t.Fatal("local packed store became program-lived")
	}
	root := &EnumType{Name: "Root", Variants: []*EnumVariant{{Name: "Safe", Payload: []Type{&SViewType{Region: "source"}}}}}
	child := &EnumType{Name: "Child", Parent: root, Variants: []*EnumVariant{{Name: "Unsafe", Payload: []Type{&SViewType{}}}}}
	root.Children = []*EnumType{child}
	if a.typeViewsProgramLived(root, map[Type]bool{}) {
		t.Fatal("unsafe refinement payload was ignored")
	}
	child.Variants[0].Payload = []Type{&SViewType{Region: "source"}}
	child.Packed = true
	if a.typeViewsProgramLived(root, map[Type]bool{}) {
		t.Fatal("local packed refinement store became program-lived")
	}
	root.Children = nil
	root.Common = map[string]Field{"view": {Name: "view", Type: &SViewType{}}}
	if a.typeViewsProgramLived(root, map[Type]bool{}) {
		t.Fatal("unsafe common field was ignored")
	}
}

func TestProgramLivedEnumWrapperOfDeclaredStore(t *testing.T) {
	node := &EnumType{Name: "Node", Packed: true, Decl: &ast.EnumDecl{StoreRegion: "source"}, Variants: []*EnumVariant{{Name: "Text", Payload: []Type{&SViewType{Region: "source"}}}}}
	wrapper := &EnumType{Name: "Wrapper", Variants: []*EnumVariant{{Name: "Node", Payload: []Type{node}}}}
	a := &Analyzer{programRegions: map[string]bool{"source": true}, namedTypes: map[string]Type{"Node": node, "Wrapper": wrapper}}
	if !a.typeViewsProgramLived(wrapper, map[Type]bool{}) {
		t.Fatal("ordinary wrapper lost declared program-store provenance")
	}
	node.Decl.StoreRegion = ""
	if a.typeViewsProgramLived(wrapper, map[Type]bool{}) {
		t.Fatal("ordinary wrapper lost a local store's ownership constraint")
	}
}
