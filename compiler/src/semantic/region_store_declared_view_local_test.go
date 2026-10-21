package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestStorePayloadDeclaredProgramViewBeforeInitializer(t *testing.T) {
	const source = `global region source
struct NameSet:
    names: darray[sview]
extern stable_name(names: NameSet&) -> sview @source
extern borrowed_name(names: NameSet&) -> sview
extern contains(names: NameSet&) -> bool
def declared(names: NameSet&) -> void:
    text: sview @source = stable_name(names) if contains(names) else ""
def borrowed(names: NameSet&) -> void:
    text: sview = borrowed_name(names) if contains(names) else ""
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "declared_view_payload.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		for _, name := range []string{"declared", "borrowed"} {
			fn := storeScanFindFunc(t, result, name)
			proven := a.storeScanStoredElementExprProgramLived(fn, &ast.Ident{Name: "text"}, map[string]bool{"text": true})
			if proven != (name == "declared") {
				t.Errorf("cold=%v %s program payload proof=%v", cold, name, proven)
			}
		}
	}
}

func TestStorePayloadDeclaredProgramViewStillChecksInitializer(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "invalid_declared_view_payload.elisa", `global region source
def invalid(text: sview) -> void:
    retained: sview @source = text
`, AnalyzeOptions{})
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `is not known to outlive region "source"`) {
		t.Fatalf("missing declared-local initializer rejection: %v", result.Errors())
	}
}
