package semantic

import (
	"fmt"
	"maps"
	"reflect"

	"elisacore/src/ast"
)

// Construction-only immutable outer baseline. Each inherited outer key is
// deep-cloned independently before mutable access, exactly like the former
// eager clone. Later explicit rekeys may alias owned maps, just as before.
type analyzerStoreScanBinderOverlay struct {
	base    map[*ast.FuncDecl]map[string]storeScanBinderVerdict
	deleted map[*ast.FuncDecl]bool
}

func (a *Analyzer) ownedStoreScanBinderRow(decl *ast.FuncDecl) (map[string]storeScanBinderVerdict, bool) {
	if row, present := a.storeScanBinderTypes[decl]; present {
		return row, true
	}
	overlay := a.storeScanBinderOverlay
	if overlay == nil || overlay.deleted[decl] {
		return nil, false
	}
	row, present := overlay.base[decl]
	if !present {
		return nil, false
	}
	owned := deepCloneValue(reflect.ValueOf(row)).Interface().(map[string]storeScanBinderVerdict)
	a.installOwnedStoreScanBinderRow(decl, owned)
	return owned, true
}

func (a *Analyzer) installOwnedStoreScanBinderRow(decl *ast.FuncDecl, row map[string]storeScanBinderVerdict) {
	if a.storeScanBinderTypes == nil {
		a.storeScanBinderTypes = map[*ast.FuncDecl]map[string]storeScanBinderVerdict{}
	}
	a.storeScanBinderTypes[decl] = row
}

func (a *Analyzer) mutableStoreScanBinderCache(decl *ast.FuncDecl) map[string]storeScanBinderVerdict {
	row, _ := a.ownedStoreScanBinderRow(decl)
	if row == nil {
		row = map[string]storeScanBinderVerdict{}
		a.installOwnedStoreScanBinderRow(decl, row)
	}
	return row
}

func (a *Analyzer) rekeyStoreScanBinderRow(from, to *ast.FuncDecl, move bool) {
	// This must precede every generic rekey. Clone the complete inherited FROM
	// before aliasing it, including a cached negative or present nil map row.
	row, present := a.ownedStoreScanBinderRow(from)
	if !present {
		return
	}
	a.installOwnedStoreScanBinderRow(to, row)
	if move {
		delete(a.storeScanBinderTypes, from)
		if overlay := a.storeScanBinderOverlay; overlay != nil {
			if overlay.deleted == nil {
				overlay.deleted = map[*ast.FuncDecl]bool{}
			}
			overlay.deleted[from] = true
		}
	}
}

func cloneAnalyzerStoreScanBinderOverlay(source *analyzerStoreScanBinderOverlay) *analyzerStoreScanBinderOverlay {
	if source == nil {
		return nil
	}
	return &analyzerStoreScanBinderOverlay{base: source.base, deleted: maps.Clone(source.deleted)}
}

func (a *Analyzer) rejectStoreScanBinderOverlayOperation(operation string) error {
	err := fmt.Errorf("specialization store-scan binder overlay cannot enter %s", operation)
	a.appendDiagnostic(Diagnostic{Severity: DiagnosticSeverityError, Message: err.Error()})
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.err = err
	}
	return err
}

var storeScanBinderRootFieldIndex = storeScanBinderFieldNumber()

func storeScanBinderFieldNumber() int {
	field, _ := reflect.TypeFor[Analyzer]().FieldByName("storeScanBinderTypes")
	return field.Index[0]
}
func storeScanBinderFieldPath(path []int) bool {
	return len(path) == 1 && path[0] == storeScanBinderRootFieldIndex
}
