package semantic

import (
	"fmt"
	"maps"

	"elisacore/src/ast"
)

// Construction-only. The canonical map is stable during this constructor,
// serialized with analyzer-backed Result queries by the shared codegen mutex.
// Values retain the exact old shallow-copy policy, including Extent aliases.
type analyzerExprFactsOverlay struct {
	base    map[ast.Expr]OptimizationFacts
	symbols map[*Symbol]OptimizationFacts
	deleted map[ast.Expr]bool
}

// Symbol fact writes remain private to the construction analyzer. The base
// follows the same frozen canonical lifetime as expression facts.
func (a *Analyzer) lookupSymbolFacts(sym *Symbol) (OptimizationFacts, bool) {
	if a == nil {
		return OptimizationFacts{}, false
	}
	if facts, ok := a.symbolFacts[sym]; ok {
		return facts, true
	}
	if a.exprFactsOverlay != nil {
		facts, ok := a.exprFactsOverlay.symbols[sym]
		return facts, ok
	}
	return OptimizationFacts{}, false
}

func (a *Analyzer) lookupExprFacts(expr ast.Expr) (OptimizationFacts, bool) {
	if a == nil {
		return OptimizationFacts{}, false
	}
	if facts, present := a.exprFacts[expr]; present {
		return facts, true
	}
	overlay := a.exprFactsOverlay
	if overlay == nil || overlay.deleted[expr] {
		return OptimizationFacts{}, false
	}
	facts, present := overlay.base[expr]
	return facts, present
}

func (a *Analyzer) exprFact(expr ast.Expr) OptimizationFacts {
	facts, _ := a.lookupExprFacts(expr)
	return facts
}

func (a *Analyzer) hasExprFactsStorage() bool {
	return a != nil && (a.exprFacts != nil || a.exprFactsOverlay != nil && a.exprFactsOverlay.base != nil)
}

func (a *Analyzer) setExprFacts(expr ast.Expr, facts OptimizationFacts) {
	if a.exprFacts == nil {
		a.exprFacts = map[ast.Expr]OptimizationFacts{}
	}
	a.exprFacts[expr] = facts
}

func (a *Analyzer) deleteExprFacts(expr ast.Expr) {
	if a == nil {
		return
	}
	delete(a.exprFacts, expr)
	if overlay := a.exprFactsOverlay; overlay != nil {
		if overlay.deleted == nil {
			overlay.deleted = map[ast.Expr]bool{}
		}
		overlay.deleted[expr] = true
	}
}

// Logical iteration preserves present zero facts and local precedence over a
// tombstone. No base materialization occurs; order remains unspecified.
func (a *Analyzer) rangeExprFacts(yield func(ast.Expr, OptimizationFacts) bool) {
	if a == nil || yield == nil {
		return
	}
	for expr, facts := range a.exprFacts {
		if !yield(expr, facts) {
			return
		}
	}
	if overlay := a.exprFactsOverlay; overlay != nil {
		for expr, facts := range overlay.base {
			if _, local := a.exprFacts[expr]; local || overlay.deleted[expr] {
				continue
			}
			if !yield(expr, facts) {
				return
			}
		}
	}
}

func (a *Analyzer) exprFactsLen() int {
	if a == nil {
		return 0
	}
	if a.exprFactsOverlay == nil {
		return len(a.exprFacts)
	}
	count := 0
	a.rangeExprFacts(func(ast.Expr, OptimizationFacts) bool { count++; return true })
	return count
}

func cloneAnalyzerExprFactsOverlay(source *analyzerExprFactsOverlay) *analyzerExprFactsOverlay {
	if source == nil {
		return nil
	}
	return &analyzerExprFactsOverlay{base: source.base, symbols: source.symbols, deleted: maps.Clone(source.deleted)}
}

func (a *Analyzer) rejectExprFactsOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization expression-facts overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
