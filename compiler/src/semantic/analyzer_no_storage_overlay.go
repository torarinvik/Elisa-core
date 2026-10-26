package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

// Specialization construction reads canonical memo/fact bases and publishes new
// rows only into its private local maps.
type analyzerNoStorageOverlay struct {
	base        map[Type]bool
	sharesBase  map[Type]bool
	helperCalls map[*ast.CallExpr]string
}

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

func (a *Analyzer) lookupStoreScanShares(t Type) (bool, bool) {
	if value, present := a.storeScanSharesCache[t]; present {
		return value, true
	}
	if a.noStorageOverlay != nil {
		value, present := a.noStorageOverlay.sharesBase[t]
		return value, present
	}
	return false, false
}

func (a *Analyzer) setStoreScanShares(t Type, value bool) {
	if a.storeScanSharesCache == nil {
		a.storeScanSharesCache = map[Type]bool{}
	}
	a.storeScanSharesCache[t] = value
}

func (a *Analyzer) lookupCompilerBuiltinHelperCall(call *ast.CallExpr) (string, bool) {
	if a == nil || call == nil {
		return "", false
	}
	if name, present := a.compilerBuiltinHelperCalls[call]; present {
		return name, true
	}
	if a.noStorageOverlay != nil {
		name, present := a.noStorageOverlay.helperCalls[call]
		return name, present
	}
	return "", false
}

func (a *Analyzer) setCompilerBuiltinHelperCall(call *ast.CallExpr, name string) {
	if a.compilerBuiltinHelperCalls == nil {
		a.compilerBuiltinHelperCalls = make(map[*ast.CallExpr]string)
	}
	a.compilerBuiltinHelperCalls[call] = name
}

func cloneAnalyzerNoStorageOverlay(source *analyzerNoStorageOverlay) *analyzerNoStorageOverlay {
	if source == nil {
		return nil
	}
	return &analyzerNoStorageOverlay{base: source.base, sharesBase: source.sharesBase, helperCalls: source.helperCalls}
}
func (a *Analyzer) rejectNoStorageOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization no-storage overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
