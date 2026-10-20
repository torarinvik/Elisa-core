package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// parallelWorkerState is a worker's view of declaration ownership during parallel
// body analysis. A worker writes only into the bodies it owns; anything it must
// analyze on another declaration's behalf (a quiet provisional-summary pass) runs
// on a private copy taken from the immutable snapshot made at fork time.
type parallelWorkerState struct {
	worker    int
	snapshots []*ast.SyntaxSnapshot // per body declaration, taken at fork time
	// at locates a body declaration in the snapshot: top-level index, and member
	// index for an impl method (-1 for a top-level function).
	at       map[*ast.FuncDecl][2]int
	owner    map[*ast.FuncDecl]int
	shadows  map[*ast.FuncDecl]*ast.FuncDecl
	original map[*ast.FuncDecl]*ast.FuncDecl
}

var funcDeclPtrType = reflect.TypeOf((*ast.FuncDecl)(nil))

// quietAnalysisTarget returns the declaration a quiet pass should analyze and a
// func to run afterwards. Off the parallel path, or for an owned declaration, that
// is decl itself.
func (a *Analyzer) quietAnalysisTarget(decl *ast.FuncDecl) (*ast.FuncDecl, func()) {
	p := a.parallel
	if p == nil {
		return decl, func() {}
	}
	owner, known := p.owner[decl]
	if !known || owner == p.worker {
		return decl, func() {}
	}
	shadow := p.shadows[decl]
	if shadow == nil {
		shadow = p.copyDecl(decl)
		if shadow == nil {
			return decl, func() {}
		}
		p.shadows[decl] = shadow
		p.original[shadow] = decl
	}
	a.rekeyFuncDeclEntries(decl, shadow, false)
	wasActive := a.funcAnalysisActive[decl]
	a.funcAnalysisActive[decl] = true
	return shadow, func() {
		a.funcAnalysisActive[decl] = wasActive
		a.rekeyFuncDeclEntries(shadow, decl, true)
	}
}

func (p *parallelWorkerState) copyDecl(decl *ast.FuncDecl) *ast.FuncDecl {
	loc, ok := p.at[decl]
	if !ok {
		return nil
	}
	snap := p.snapshots[loc[0]]
	if snap == nil {
		return nil
	}
	copied, err := snap.CopyDeclaration(0)
	if err != nil {
		return nil
	}
	if loc[1] < 0 {
		fn, _ := copied.(*ast.FuncDecl)
		return fn
	}
	impl, ok := copied.(*ast.ImplDecl)
	if !ok || loc[1] >= len(impl.Members) {
		return nil
	}
	fn, _ := impl.Members[loc[1]].(*ast.FuncDecl)
	return fn
}

// rekeyFuncDeclEntries copies every Analyzer map entry keyed by from to the key to,
// in maps at any depth reachable through struct fields. With move set the from
// entries are deleted, so a shadow never outlives its pass.
func (a *Analyzer) rekeyFuncDeclEntries(from, to *ast.FuncDecl, move bool) {
	fromKey, toKey := reflect.ValueOf(from), reflect.ValueOf(to)
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Struct:
			if !typeHoldsMapInline(v.Type()) {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				visit(settable(v.Field(i)))
			}
		case reflect.Map:
			if v.IsNil() || v.Type().Key() != funcDeclPtrType {
				return
			}
			if val := v.MapIndex(fromKey); val.IsValid() {
				v.SetMapIndex(toKey, val)
				if move {
					v.SetMapIndex(fromKey, reflect.Value{})
				}
			}
		}
	}
	visit(settable(reflect.ValueOf(a).Elem()))
}

// ownsKey reports whether a worker's entry under key may be merged: entries keyed
// by a body declaration come only from the worker that owns it.
func (p *parallelWorkerState) ownsKey(key reflect.Value) bool {
	if p == nil || key.Type() != funcDeclPtrType {
		return true
	}
	decl := key.Interface().(*ast.FuncDecl)
	if _, shadow := p.original[decl]; shadow {
		return false
	}
	owner, known := p.owner[decl]
	return !known || owner == p.worker
}
