package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

// Specialization construction reads canonical memo/fact bases and publishes new
// rows only into its private local maps.
type analyzerNoStorageOverlay struct {
	base          map[Type]bool
	viewOnlyBase  map[Type]bool
	sharesBase    map[Type]bool
	paramWrites   map[storeScanParamKey]bool
	helperCalls   map[*ast.CallExpr]string
	returnUnknown map[*ast.ReturnStmt]bool
}

func (a *Analyzer) lookupStoreScanViewOnly(t Type) (bool, bool) {
	if value, present := a.storeScanViewOnlyCache[t]; present {
		return value, true
	}
	if a.noStorageOverlay != nil {
		value, present := a.noStorageOverlay.viewOnlyBase[t]
		return value, present
	}
	return false, false
}

func (a *Analyzer) setStoreScanViewOnly(t Type, value bool) {
	if a.storeScanViewOnlyCache == nil {
		a.storeScanViewOnlyCache = make(map[Type]bool)
	}
	a.storeScanViewOnlyCache[t] = value
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

func (a *Analyzer) lookupStoreScanParamWriteCache(key storeScanParamKey) (bool, bool) {
	if value, present := a.storeScanParamWriteCache[key]; present {
		return value, true
	}
	if a.noStorageOverlay != nil {
		value, present := a.noStorageOverlay.paramWrites[key]
		return value, present
	}
	return false, false
}

func (a *Analyzer) setStoreScanParamWriteCache(key storeScanParamKey, value bool) {
	if a.storeScanParamWriteCache == nil {
		a.storeScanParamWriteCache = make(map[storeScanParamKey]bool)
	}
	a.storeScanParamWriteCache[key] = value
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

func (a *Analyzer) lookupReturnElementStmtUnknown(ret *ast.ReturnStmt) (bool, bool) {
	if a == nil || ret == nil {
		return false, false
	}
	if unknown, present := a.returnElementStmtUnknown[ret]; present {
		return unknown, true
	}
	if a.noStorageOverlay != nil {
		unknown, present := a.noStorageOverlay.returnUnknown[ret]
		return unknown, present
	}
	return false, false
}

func (a *Analyzer) setReturnElementStmtUnknown(ret *ast.ReturnStmt) {
	if a.returnElementStmtUnknown == nil {
		a.returnElementStmtUnknown = make(map[*ast.ReturnStmt]bool)
	}
	a.returnElementStmtUnknown[ret] = true
}

func (a *Analyzer) clearReturnElementStmtUnknown(ret *ast.ReturnStmt) {
	if a == nil || ret == nil {
		return
	}
	if a.noStorageOverlay == nil {
		delete(a.returnElementStmtUnknown, ret)
		return
	}
	if _, inherited := a.noStorageOverlay.returnUnknown[ret]; inherited {
		if a.returnElementStmtUnknown == nil {
			a.returnElementStmtUnknown = make(map[*ast.ReturnStmt]bool)
		}
		// Mask an inherited true row when removing a canonical-keyed entry.
		a.returnElementStmtUnknown[ret] = false
		return
	}
	delete(a.returnElementStmtUnknown, ret)
}

func cloneAnalyzerNoStorageOverlay(source *analyzerNoStorageOverlay) *analyzerNoStorageOverlay {
	if source == nil {
		return nil
	}
	return &analyzerNoStorageOverlay{base: source.base, sharesBase: source.sharesBase, paramWrites: source.paramWrites, helperCalls: source.helperCalls, returnUnknown: source.returnUnknown}
}
func (a *Analyzer) rejectNoStorageOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization no-storage overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
