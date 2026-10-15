package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestSMTImmutableSviewSuffixIndex(t *testing.T) {
	for _, result := range []string{"suffix", "suffix + 1"} {
		source := "def has_suffix(text: sview, suffix: u8) -> bool:\n    ensure result == (text.len > 0 and text[text.len - 1] == suffix)\n    return text.len > 0 and text[text.len - 1] == " + result + "\n"
		r := analyzeContractStrict(t, "sview_suffix.elisa", source)
		rejected := strings.Contains(allDiagnostics(r), "could not be proven")
		if (result == "suffix" && len(r.Errors()) != 0) || rejected != (result != "suffix") {
			t.Fatalf("result %s: %s", result, allDiagnostics(r))
		}
	}
}

func TestSMTSviewIndexDeclinesMutableAndLocalBindings(t *testing.T) {
	sources := []string{
		// The return differs syntactically from the clause (`>= 1`): an identical return holds by
		// reflexivity at the return point (both read the current binding), which is sound and is
		// not what this test is about. The SMT sview model itself must decline the mutable binding.
		"def f(text: mutable sview) -> bool:\n    ensure result == (text.len > 0 and text[0] == 65)\n    return text.len >= 1 and text[0] == 65\n",
		"def f(text: sview) -> bool:\n    ensure result == (text.len > 0 and text[0] == 65)\n    local: sview = text\n    return local.len > 0 and local[0] == 65\n",
	}
	for _, source := range sources {
		r := analyzeContractStrict(t, "unstable_sview.elisa", source)
		if !strings.Contains(allDiagnostics(r), "could not be proven") {
			t.Fatalf("unstable binding accepted: %s", allDiagnostics(r))
		}
	}
}

func TestSMTSviewIndexDoesNotReusePreMutationSnapshot(t *testing.T) {
	source := `def wrong(text: sview, storage: mutable darray[u8]&) -> bool:
    requires storage.count > 0
    ensure result == (text.len > 0 and text[0] == 65)
    before: bool = text.len > 0 and text[0] == 65
    storage[0] <- 66
    return before
`
	r := analyzeContractStrict(t, "sview_alias_mutation.elisa", source)
	if !strings.Contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("old byte snapshot accepted: %s", allDiagnostics(r))
	}
}

func TestSMTSviewIndexRejectsSubstitutionShadowingAndCalls(t *testing.T) {
	for _, name := range []string{"plain", "substitution", "string_substitution", "shadow", "callee", "call", "overloaded"} {
		t.Run(name, func(t *testing.T) {
			id := &ast.Ident{Name: "text"}
			function := &ast.FuncDecl{Params: []ast.ParamDecl{{Name: "text"}}, Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.BoolLit{Value: true}}}}
			analyzer := &Analyzer{currentFuncDecl: function, currentScope: NewScope(nil)}
			analyzer.currentScope.Define(&Symbol{Name: "text", Kind: SymbolParam, Type: &SViewType{}, Node: function, ParamIndex: 0})
			translator := &smtTranslator{a: analyzer}
			var env map[string]string
			switch name {
			case "substitution":
				env = map[string]string{"text": "other"}
			case "string_substitution":
				env = map[string]string{"\x00string:text": "other"}
			case "shadow":
				analyzer.currentScope = NewScope(analyzer.currentScope)
				analyzer.currentScope.Define(&Symbol{Name: "text", Kind: SymbolLocal, Type: &SViewType{}})
			case "callee":
				translator.calleeBodyDepth = 1
			case "call":
				function.Body = []ast.Stmt{&ast.ReturnStmt{Value: &ast.CallExpr{}}}
			case "overloaded":
				function.Body = []ast.Stmt{&ast.ReturnStmt{Value: &ast.BinaryExpr{Op: lexer.TOKEN_EQEQ, Left: &ast.IntLit{Value: "1"}, Right: &ast.IntLit{Value: "1"}, LoweredCall: &ast.CallExpr{}}}}
			}
			if got := translator.immutableSviewArrayParameter(id, env); got != (name == "plain") {
				t.Fatalf("accepted=%v", got)
			}
		})
	}
}
