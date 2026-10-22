package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestSMTFactSnapshotsProtectSpareCapacityAndWriteInvalidation(t *testing.T) {
	fact := func(name string) smtFact {
		return smtFact{Expr: &ast.Ident{Name: name}, Deps: map[string]bool{name: true}}
	}
	original := make([]smtFact, 2, 8)
	original[0] = fact("box")
	original[1] = fact("other")
	scope := &Scope{smtAssertFacts: original}
	a := &Analyzer{currentScope: scope}
	entry := a.snapshotSMTFactFrame()
	scope.smtAssertFacts = append(scope.smtAssertFacts, fact("first"))
	first := a.restoreSMTFactFrame(entry)
	scope.smtAssertFacts = append(scope.smtAssertFacts, fact("second"))
	a.restoreSMTFactFrame(entry)
	if !first.facts[0][2].Deps["first"] {
		t.Fatal("sibling append overwrote captured branch")
	}
	a.dropSMTFactsOverlappingWrites("box", [][]string{{"field"}})
	left := a.restoreSMTFactFrame(entry)
	a.dropSMTFactsOverlappingWrites("other", [][]string{{"field"}})
	right := a.restoreSMTFactFrame(entry)
	if len(left.facts[0]) != 1 || !left.facts[0][0].Deps["other"] || len(right.facts[0]) != 1 || !right.facts[0][0].Deps["box"] {
		t.Fatal("write invalidation leaked between branches")
	}
	if len(entry.facts[0]) != 2 || !entry.facts[0][0].Deps["box"] || !entry.facts[0][1].Deps["other"] {
		t.Fatal("write invalidation changed entry")
	}
}

func TestDropSMTFactsCopyOnWritePreservesInput(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		input := []smtFact{{Expr: &ast.Ident{Name: "a"}}, {Expr: &ast.Ident{Name: "b"}}, {Expr: &ast.Ident{Name: "c"}}, {Expr: &ast.Ident{Name: "d"}}}
		wantInput := append([]smtFact(nil), input...)
		var want []smtFact
		for i, f := range input {
			if mask&(1<<i) == 0 {
				want = append(want, f)
			}
		}
		got := dropSMTFactsCopyOnWrite(input, func(f smtFact) bool { return mask&(1<<int(f.Expr.(*ast.Ident).Name[0]-'a')) != 0 })
		if len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, want) {
			t.Fatalf("mask %d wrong survivors", mask)
		}
		if mask == 0 {
			if &got[0] != &input[0] {
				t.Fatal("unchanged list copied")
			}
		} else if len(got) > 0 {
			got[0] = smtFact{}
		}
		if !reflect.DeepEqual(input, wantInput) {
			t.Fatalf("mask %d changed input", mask)
		}
	}
}
