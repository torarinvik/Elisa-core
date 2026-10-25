package semantic

import "fmt"

// Constructor-only bool lookup base. Never recompute an inherited positive or
// negative entry; new entries are published to the private local map only.
type analyzerNoStorageOverlay struct{ base map[Type]bool }

func (a *Analyzer) lookupStoreScanNoStorage(t Type) (bool, bool) {
	if value, present := a.storeScanNoStorageCache[t]; present {
		return value, true
	}
	if a.noStorageOverlay != nil {
		value, present := a.noStorageOverlay.base[t]
		return value, present
	}
	return false, false
}
func (a *Analyzer) setStoreScanNoStorage(t Type, value bool) {
	if a.storeScanNoStorageCache == nil {
		a.storeScanNoStorageCache = map[Type]bool{}
	}
	a.storeScanNoStorageCache[t] = value
}
func cloneAnalyzerNoStorageOverlay(source *analyzerNoStorageOverlay) *analyzerNoStorageOverlay {
	if source == nil {
		return nil
	}
	return &analyzerNoStorageOverlay{base: source.base}
}
func (a *Analyzer) rejectNoStorageOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization no-storage overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
