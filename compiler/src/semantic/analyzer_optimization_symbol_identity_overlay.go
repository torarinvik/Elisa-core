package semantic

import "fmt"

// Construction-only read-only canonical base plus private local strings. The
// shared codegen mutex keeps canonical maps stable while the constructor runs.
// Existing immutable IDs are never regenerated; a miss uses the unchanged
// shared atomic allocator and writes only the private local table.
type analyzerOptimizationSymbolIdentityOverlay struct{ base map[*Symbol]string }

func (a *Analyzer) lookupOptimizationSymbolIdentity(symbol *Symbol) (string, bool) {
	if a == nil {
		return "", false
	}
	if value, present := a.optimizationSymbolIDs[symbol]; present {
		return value, true
	}
	if overlay := a.optimizationSymbolIdentityOverlay; overlay != nil {
		value, present := overlay.base[symbol]
		return value, present
	}
	return "", false
}

func (a *Analyzer) setOptimizationSymbolIdentity(symbol *Symbol, value string) {
	if a.optimizationSymbolIDs == nil {
		a.optimizationSymbolIDs = make(map[*Symbol]string)
	}
	a.optimizationSymbolIDs[symbol] = value
}

func cloneAnalyzerOptimizationSymbolIdentityOverlay(source *analyzerOptimizationSymbolIdentityOverlay) *analyzerOptimizationSymbolIdentityOverlay {
	if source == nil {
		return nil
	}
	return &analyzerOptimizationSymbolIdentityOverlay{base: source.base}
}

func (a *Analyzer) rejectOptimizationSymbolIdentityOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization optimization-symbol identity overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
