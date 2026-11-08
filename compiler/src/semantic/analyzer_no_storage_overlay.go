package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

// Specialization construction reads canonical memo/fact bases and publishes new
// rows only into its private local maps.
type analyzerNoStorageOverlay struct {
	base             map[Type]bool
	viewOnlyBase     map[Type]bool
	sharesBase       map[Type]bool
	paramWrites      map[storeScanParamKey]bool
	helperCalls      map[*ast.CallExpr]string
	returnUnknown    map[*ast.ReturnStmt]bool
	containerRegions map[Type]string
	storeModes       map[storeScanKey]storeMode
}

// Completed scan results are immutable in the canonical analyzer while
// specialization construction holds shared codegen. Private writes shadow
// the base, including storeNone, which is a present and meaningful result.
func (a *Analyzer) lookupStoreScanMode(key storeScanKey) (storeMode, bool) {
	if mode, ok := a.storeScanCache[key]; ok {
		return mode, true
	}
	canonicalKey, share, root := a.specializationStoreScanKey(key)
	if root {
		// The current function is being analyzed under concrete type arguments;
		// its canonical store mode may not describe this specialization.
		return storeNone, false
	}
	if a.noStorageOverlay != nil {
		baseKey := key
		if share {
			baseKey = canonicalKey
		}
		if mode, ok := a.noStorageOverlay.storeModes[baseKey]; ok {
			return mode, true
		}
	}
	if share && a.parallel.specialization.base != nil && a.parallel.specialization.base.shared != nil {
		return a.parallel.specialization.base.shared.lookupStoreScanMode(canonicalKey)
	}
	return storeNone, false
}

// specializationStoreScanKey maps read-only copied callees to their canonical
// cache key. The specialization target is deliberately excluded because its
// concrete argument types can change store behavior.
func (a *Analyzer) specializationStoreScanKey(key storeScanKey) (canonical storeScanKey, share, root bool) {
	canonical = key
	if a == nil || a.parallel == nil || a.parallel.specialization == nil {
		return canonical, false, false
	}
	context := a.parallel.specialization
	if original := a.parallel.original[key.decl]; original != nil {
		canonical.decl = original
	}
	if context.targetDecl != nil && canonical.decl == context.targetDecl {
		return canonical, false, true
	}
	return canonical, true, false
}

func (a *Analyzer) noteSpecializationStoreScanTarget(decl *ast.FuncDecl) {
	if a == nil || a.parallel == nil || a.parallel.specialization == nil {
		return
	}
	_, _, root := a.specializationStoreScanKey(storeScanKey{decl: decl})
	if root {
		a.parallel.specialization.storeScanTargetHit = true
	}
}

func (a *Analyzer) publishSpecializationStoreScanMode(key storeScanKey, mode storeMode) {
	canonical, share, root := a.specializationStoreScanKey(key)
	if !share || root || a.parallel.specialization.base == nil || a.parallel.specialization.base.shared == nil {
		return
	}
	a.parallel.specialization.base.shared.cacheStoreScanMode(canonical, mode)
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
	return &analyzerNoStorageOverlay{base: source.base, viewOnlyBase: source.viewOnlyBase, sharesBase: source.sharesBase, paramWrites: source.paramWrites, helperCalls: source.helperCalls, returnUnknown: source.returnUnknown, containerRegions: source.containerRegions, storeModes: source.storeModes}
}
func (a *Analyzer) rejectNoStorageOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization no-storage overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}
