package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"maps"
)

// Construction-only. The base table is immutable during this constructor under
// the shared codegen lock; all semantic writes use Analyzer.exprTypes locally.
// This resource must never enter a canonical Result or persisted analysis.
type analyzerExprTypeOverlay struct {
	base    map[ast.Expr]Type
	deleted map[ast.Expr]bool
}

func (a *Analyzer) lookupExprType(expr ast.Expr) (Type, bool) {
	if a == nil {
		return nil, false
	}
	if value, present := a.exprTypes[expr]; present {
		return value, true
	}
	overlay := a.exprTypeOverlay
	if overlay == nil || overlay.deleted[expr] {
		return nil, false
	}
	value, present := overlay.base[expr]
	return value, present
}
func (a *Analyzer) exprType(expr ast.Expr) Type { value, _ := a.lookupExprType(expr); return value }
func (a *Analyzer) deleteExprType(expr ast.Expr) {
	if a == nil {
		return
	}
	delete(a.exprTypes, expr)
	if overlay := a.exprTypeOverlay; overlay != nil {
		if overlay.deleted == nil {
			overlay.deleted = map[ast.Expr]bool{}
		}
		overlay.deleted[expr] = true
	}
}

// Explicit logical iteration, not materialization. Local present nils shadow
// base entries; deleted base keys stay absent. Order, like a Go map, is unspecified.
func (a *Analyzer) rangeExprTypes(yield func(ast.Expr, Type) bool) {
	if a == nil || yield == nil {
		return
	}
	for expr, typ := range a.exprTypes {
		if !yield(expr, typ) {
			return
		}
	}
	if overlay := a.exprTypeOverlay; overlay != nil {
		for expr, typ := range overlay.base {
			if _, local := a.exprTypes[expr]; local || overlay.deleted[expr] {
				continue
			}
			if !yield(expr, typ) {
				return
			}
		}
	}
}

func cloneAnalyzerExprTypeOverlay(source *analyzerExprTypeOverlay) *analyzerExprTypeOverlay {
	if source == nil {
		return nil
	}
	return &analyzerExprTypeOverlay{base: source.base, deleted: maps.Clone(source.deleted)}
}

func (a *Analyzer) rejectExprTypeOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization expression-type overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
