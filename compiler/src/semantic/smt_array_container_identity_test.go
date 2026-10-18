//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"os"
	"strings"
	"testing"
)

func TestSMTStableContainerDeclaredFlatArray(t *testing.T) {
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: "lines", Kind: SymbolParam, Type: &RefType{Elem: &DArrayType{Elem: &BuiltinType{Name: "u32"}}}})
	a := &Analyzer{currentScope: scope, exprTypes: map[ast.Expr]Type{}}
	tr := a.newSMTTranslator(nil)
	actual, ok := tr.arrayTermEnv(&ast.Ident{Name: "lines"}, nil)
	if !ok {
		t.Fatal("actual integer array declined")
	}
	tr.markStableContainer(actual)
	tr.calleeBodyDepth = 1
	formal := &ast.Ident{Name: "formal"}
	if got, ok := tr.arrayTermEnv(formal, map[string]string{"formal": actual}); !ok || got != actual {
		t.Fatalf("declared flat array lost through stable identity: %q %v", got, ok)
	}
	tr.markStableContainer("opaque_container")
	if got, ok := tr.arrayTermEnv(formal, map[string]string{"formal": "opaque_container"}); ok {
		t.Fatalf("opaque container treated as flat array: %s", got)
	}
	tr.lengthOnly = map[string]bool{actual: true}
	if got, ok := tr.arrayTermEnv(formal, map[string]string{"formal": actual}); ok {
		t.Fatalf("length-only binding admitted despite declaration: %s", got)
	}
	if tr.declaredFlatArrayTerm(actual + "_suffix") {
		t.Fatal("partial declaration name matched")
	}
	if _, ok := tr.arrayTermEnv(formal, nil); ok {
		t.Fatal("unbound callee root borrowed caller identity")
	}
}

func TestSMTAssociatedLookupArrayIdentity(t *testing.T) {
	for _, path := range []string{"testdata/smt_assoc_bound_marker_original.elisa", "testdata/smt_assoc_bound_marker_early.elisa", "testdata/smt_assoc_membership_early.elisa"} {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := string(data)
			result := analyzeContractStrict(t, "associated_lookup.elisa", source)
			if es := result.Errors(); len(es) != 0 {
				t.Fatal(es)
			}
			var old, wrong string
			if strings.Contains(path, "original") {
				old = "found <- true"
				wrong = "found <- false"
			} else {
				old = "                return true if"
				wrong = "                return false if"
			}
			if strings.Count(source, old) != 1 {
				t.Fatal("lookup hit missing or ambiguous")
			}
			mutant := strings.Replace(source, old, wrong, 1)
			rejected := analyzeContractStrict(t, "associated_lookup_wrong.elisa", mutant)
			if !strings.Contains(strings.Join(rejected.Errors(), "\n"), "could not be proven") {
				t.Fatalf("false lookup hit accepted or wrong error: %v", rejected.Errors())
			}
		})
	}
}
