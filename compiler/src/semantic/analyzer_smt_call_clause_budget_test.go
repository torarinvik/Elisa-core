//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestCallClauseBudgetKeepsIntegerCallsCongruent(t *testing.T) {
	result, call := budgetTestCall(t, `def value(x: i64) -> i64:
    requires x >= 0
    ensure result >= 0
    return x
def main() -> i32:
    item: i64 = value(1)
    return 0
`, "value")
	result.analyzer.currentScope = result.GlobalScope
	tr := result.analyzer.newSMTTranslator(nil)
	tr.callClauseExpansions = smtCallClauseMaxExpansions
	first, ok := tr.callResultTermEnv(call, nil)
	if !ok {
		t.Fatal("integer call should lower to an opaque result at the clause-expansion bound")
	}
	second, ok := tr.callResultTermEnv(call, nil)
	if !ok || first != second {
		t.Fatalf("repeated integer call at the expansion bound must reuse its canonical result: %q then %q (ok=%v)", first, second, ok)
	}
}

func TestCallClauseBudgetKeepsBooleanCallsCongruent(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "call_clause_budget_bool.elisa", `def ready(x: i64) -> bool:
    requires x >= 0
    ensure result == (x > 0)
    return x > 0
def main() -> i32:
    if ready(1):
        return 1
    return 0
`, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("test source should analyze cleanly: %v", errs)
	}
	var call *ast.CallExpr
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "main" {
			continue
		}
		for _, stmt := range fn.Body {
			if branch, ok := stmt.(*ast.IfStmt); ok {
				result.analyzer.walkStaticExpr(branch.Cond, func(expr ast.Expr) bool {
					if candidate, ok := expr.(*ast.CallExpr); ok {
						call = candidate
						return true
					}
					return false
				})
			}
		}
	}
	if call == nil {
		t.Fatal("boolean call was not found in the parsed source")
	}
	result.analyzer.currentScope = result.GlobalScope
	tr := result.analyzer.newSMTTranslator(nil)
	tr.callClauseExpansions = smtCallClauseMaxExpansions
	first, ok := tr.callResultBoolTermEnv(call, nil)
	if !ok {
		t.Fatal("boolean call should lower to an opaque result at the clause-expansion bound")
	}
	second, ok := tr.callResultBoolTermEnv(call, nil)
	if !ok || first != second {
		t.Fatalf("repeated boolean call at the expansion bound must reuse its canonical result: %q then %q (ok=%v)", first, second, ok)
	}
}

func TestCallClauseBudgetDoesNotCountCallsWithoutContracts(t *testing.T) {
	result, call := budgetTestCall(t, `def value(x: i64) -> i64:
    return x
def main() -> i32:
    item: i64 = value(1)
    return 0
`, "value")
	result.analyzer.currentScope = result.GlobalScope
	tr := result.analyzer.newSMTTranslator(nil)
	tr.callClauseExpansions = smtCallClauseMaxExpansions
	value, ok := tr.callResultTermEnv(call, nil)
	if !ok {
		t.Fatal("direct function call should lower even when contract-expansion fuel is exhausted")
	}
	if tr.callClauseExpansions != smtCallClauseMaxExpansions {
		t.Fatalf("call without contracts consumed clause fuel: %d", tr.callClauseExpansions-smtCallClauseMaxExpansions)
	}
	if !strings.Contains(strings.Join(tr.auxDecls, ""), "(assert (= "+value+" ") {
		t.Fatal("bounded contract expansion must not suppress a no-contract function's eligible defining equation")
	}
}

func budgetTestCall(t *testing.T, source, functionName string) (*Result, *ast.CallExpr) {
	t.Helper()
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "call_clause_budget_helper.elisa", source, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("test source should analyze cleanly: %v", errs)
	}
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "main" {
			continue
		}
		for _, stmt := range fn.Body {
			if local, ok := stmt.(*ast.VarDeclStmt); ok {
				if call, ok := local.Value.(*ast.CallExpr); ok {
					if id, ok := call.Func.(*ast.Ident); ok && id.Name == functionName {
						return result, call
					}
				}
			}
		}
	}
	t.Fatal("integer call was not found in the parsed source")
	return result, nil
}
