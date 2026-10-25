package semantic

import (
	"fmt"
	"maps"
	"reflect"
)

// Construction-only. The canonical table and its inline maps are stable while
// the shared codegen mutex owns this constructor. Never return a borrowed base
// value: regionRefState contains writable maps, unlike opaque ExprFacts values.
type analyzerIterBinderElementOverlay struct {
	base    map[*Symbol]regionRefState
	deleted map[*Symbol]bool
}

func (a *Analyzer) lookupIterBinderElementState(symbol *Symbol) (regionRefState, bool) {
	if a == nil {
		return regionRefState{}, false
	}
	if value, present := a.iterBinderElementStates[symbol]; present {
		return value, true
	}
	overlay := a.iterBinderElementOverlay
	if overlay == nil || overlay.deleted[symbol] {
		return regionRefState{}, false
	}
	value, present := overlay.base[symbol]
	if !present {
		return regionRefState{}, false
	}
	// Use the EXACT previous cloning policy, not cloneRegionRefState: inline
	// maps are recursively copied, pointers stay shared, and slice backing is
	// retained with cap==len (not copied). Cache one isolated value per key.
	value = deepCloneValue(reflect.ValueOf(value)).Interface().(regionRefState)
	a.setIterBinderElementState(symbol, value)
	return value, true
}

func (a *Analyzer) setIterBinderElementState(symbol *Symbol, value regionRefState) {
	if a.iterBinderElementStates == nil {
		a.iterBinderElementStates = map[*Symbol]regionRefState{}
	}
	// Preserve normal map assignment semantics. The existing producer still
	// calls cloneRegionRefState before assignment, exactly as it did before.
	a.iterBinderElementStates[symbol] = value
}

func (a *Analyzer) deleteIterBinderElementState(symbol *Symbol) {
	if a == nil {
		return
	}
	delete(a.iterBinderElementStates, symbol)
	if overlay := a.iterBinderElementOverlay; overlay != nil {
		if overlay.deleted == nil {
			overlay.deleted = map[*Symbol]bool{}
		}
		overlay.deleted[symbol] = true
	}
}

func cloneAnalyzerIterBinderElementOverlay(source *analyzerIterBinderElementOverlay) *analyzerIterBinderElementOverlay {
	if source == nil {
		return nil
	}
	return &analyzerIterBinderElementOverlay{base: source.base, deleted: maps.Clone(source.deleted)}
}

func (a *Analyzer) rejectIterBinderElementOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization iteration-binder overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
