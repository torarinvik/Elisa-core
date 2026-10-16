package semantic

import (
	"strconv"

	"elisacore/src/ast"
)

const constTableMaxElems = 4096

// constTableIndexTerm lowers `T[i]` where T names a constant `const T: E[N] = [e0, …]` that the
// constant evaluator folds to a list of integers (const enum members included) — the shape the char-classifier fold
// synthesizes (analyzer_classifier_fold.go). The value is a piecewise term over the runs of equal
// consecutive elements, exact for every in-range index. An out-of-range index never reaches a
// value: the indexed load is bounds-checked (or the index type cannot exceed the table), so the run
// chosen for it is irrelevant to any reachable state.
func (tr *smtTranslator) constTableIndexTerm(n *ast.IndexExpr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || n == nil || n.Index2 != nil || n.Fallback != nil || n.AsSpecialize != nil || n.AsOverlayCall != nil {
		return "", false
	}
	id, ok := stripOptimizationParens(n.Object).(*ast.Ident)
	if !ok || id == nil || tr.a.currentScope == nil {
		return "", false
	}
	if _, bound := env[id.Name]; bound {
		return "", false
	}
	if sym, found := tr.a.currentScope.Lookup(id.Name); found && sym != nil && (sym.Mutable || sym.Kind == SymbolLocal || sym.Kind == SymbolParam) {
		return "", false
	}
	table, ok := tr.a.evalConstExpr(id)
	if !ok || table.Kind != ConstList || len(table.Elems) == 0 || len(table.Elems) > constTableMaxElems {
		return "", false
	}
	values := make([]int64, len(table.Elems))
	for i, elem := range table.Elems {
		if elem.Kind != ConstInt {
			return "", false
		}
		values[i] = elem.Int
	}
	idx, ok := tr.termEnv(n.Index, env)
	if !ok {
		return "", false
	}
	// Runs of equal values: [start_k, end_k] -> v_k. Build from the last run backwards.
	type run struct {
		end   int
		value int64
	}
	var runs []run
	for i, v := range values {
		if len(runs) > 0 && runs[len(runs)-1].value == v {
			runs[len(runs)-1].end = i
			continue
		}
		runs = append(runs, run{end: i, value: v})
	}
	term := smtInt(runs[len(runs)-1].value)
	for k := len(runs) - 2; k >= 0; k-- {
		term = "(ite (<= " + idx + " " + strconv.Itoa(runs[k].end) + ") " + smtInt(runs[k].value) + " " + term + ")"
	}
	return term, true
}
