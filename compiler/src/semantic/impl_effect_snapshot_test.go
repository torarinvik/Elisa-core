package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
	"testing"
)

func TestPendingImplEffectCheckRoundTripKeepsInferredTypeAlias(t *testing.T) {
	expected := &FuncType{PermissionRefs: []ast.PermissionRef{{Name: "Atomics", Member: "Load"}}}
	actual := &FuncType{PermissionRefs: []ast.PermissionRef{{Name: "Atomics", Member: "Load"}}}
	a := &Analyzer{globalScope: NewScope(nil), reportedDiagnostics: map[diagnosticIdentity]bool{}}
	a.globalScope.Define(&Symbol{Name: "implementation", Kind: SymbolFunc, Type: actual})
	a.checkImplEffectErrorFrameVariance("Reader", "read", expected, actual, nil, nil, lexer.Pos{File: "protocol.elisa", Line: 7})
	if len(a.diagnostics) != 0 || len(a.deferredImplEffectChecks) != 1 {
		t.Fatalf("check was not deferred: %+v", a.diagnostics)
	}
	data, err := semanticStateRegistry().Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := semanticStateRegistry().Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	restored := decoded.(*Analyzer)
	symbol, ok := restored.globalScope.Lookup("implementation")
	if !ok {
		t.Fatal("missing implementation")
	}
	inferred := symbol.Type.(*FuncType)
	if restored.deferredImplEffectChecks[0].actual != inferred {
		t.Fatal("pending check lost its reference to the inferred function type")
	}
	// Effect inference fills this row after declaration registration. The restored
	// obligation must observe the updated row and reject an extra member.
	inferred.PermissionRefs = append(inferred.PermissionRefs, ast.PermissionRef{Name: "Atomics", Member: "Store"})
	restored.deferredImplEffectChecks[0].run(restored)
	if len(restored.diagnostics) != 1 || !strings.Contains(restored.diagnostics[0].Message, "Atomics.Store") {
		t.Fatalf("restored check missed extra inferred effect: %+v", restored.diagnostics)
	}
	if len(a.diagnostics) != 0 || len(actual.PermissionRefs) != 1 {
		t.Fatal("restored check mutated original analysis")
	}
}
