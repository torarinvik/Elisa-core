package semantic

import (
	"reflect"
	"sync"

	"elisacore/src/ast"
)

// parallelWorkerState is a worker's view of declaration ownership during parallel
// body analysis. A worker writes only into the bodies it owns; anything it must
// analyze on another declaration's behalf (a quiet provisional-summary pass) runs
// on a private copy taken from the immutable snapshot made at fork time.
type parallelWorkerState struct {
	worker    int
	snapshots []*ast.SyntaxSnapshot // per body declaration, taken at fork time
	frozen    []ast.Decl            // pristine reflective copies where a snapshot declined
	// at locates a body declaration in the snapshot: top-level index, and member
	// index for an impl method (-1 for a top-level function).
	at          map[*ast.FuncDecl][2]int
	owner       map[*ast.FuncDecl]int
	shadows     map[*ast.FuncDecl]*ast.FuncDecl
	readShadows map[*ast.FuncDecl]*ast.FuncDecl
	original    map[*ast.FuncDecl]*ast.FuncDecl
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
	var copied ast.Decl
	if snap := p.snapshots[loc[0]]; snap != nil {
		var err error
		if copied, err = snap.CopyDeclaration(0); err != nil {
			return nil
		}
	} else if frozen := p.frozen[loc[0]]; frozen != nil {
		copied = copyASTDecl(frozen)
	} else {
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
	root := settable(reflect.ValueOf(a).Elem())
	for _, path := range funcDeclKeyedMapPaths() {
		v := root
		for _, i := range path {
			v = settable(v.Field(i))
		}
		if v.IsNil() {
			continue
		}
		if val := v.MapIndex(fromKey); val.IsValid() {
			v.SetMapIndex(toKey, val)
			if move {
				v.SetMapIndex(fromKey, reflect.Value{})
			}
		}
	}
}

// funcDeclKeyedMapPaths lists the field paths, through inline struct fields, of
// every Analyzer map keyed by *ast.FuncDecl.
var funcDeclKeyedMapPaths = sync.OnceValue(func() [][]int {
	var paths [][]int
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			ft := t.Field(i).Type
			path := append(append([]int(nil), prefix...), i)
			switch ft.Kind() {
			case reflect.Map:
				// A value that refers to declarations (call edges, components,
				// member lists) would point a copy back at the originals, or leak a
				// shadow into published state: those maps are rebuilt per key.
				if ft.Key() == funcDeclPtrType && !typeRefersToFuncDecl(ft.Elem(), map[reflect.Type]bool{}) {
					paths = append(paths, path)
				}
			case reflect.Struct:
				if typeHoldsMapInline(ft) {
					walk(ft, path)
				}
			}
		}
	}
	walk(reflect.TypeOf(Analyzer{}), nil)
	return paths
})

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

// readDecl returns the declaration a read-only body walk (a store-scan summary of
// a callee) should walk: a foreign declaration's pristine read copy, made once per
// worker and given a copy of the original's declaration-keyed entries, or decl
// itself. Read copies are never analyzed, so they stay pristine.
func (a *Analyzer) readDecl(decl *ast.FuncDecl) *ast.FuncDecl {
	p := a.parallel
	if p == nil || decl == nil {
		return decl
	}
	if _, isShadow := p.original[decl]; isShadow {
		return decl
	}
	owner, known := p.owner[decl]
	if !known || owner == p.worker {
		return decl
	}
	if shadow := p.readShadows[decl]; shadow != nil {
		return shadow
	}
	shadow := p.copyDecl(decl)
	if shadow == nil {
		return decl
	}
	if p.readShadows == nil {
		p.readShadows = map[*ast.FuncDecl]*ast.FuncDecl{}
	}
	p.readShadows[decl] = shadow
	p.original[shadow] = decl
	a.rekeyFuncDeclEntries(decl, shadow, false)
	return shadow
}

// isParallelShadow reports whether decl is a worker-private copy.
func (a *Analyzer) isParallelShadow(decl *ast.FuncDecl) bool {
	if a.parallel == nil {
		return false
	}
	_, shadow := a.parallel.original[decl]
	return shadow
}

func typeRefersToFuncDecl(t reflect.Type, seen map[reflect.Type]bool) bool {
	if t == funcDeclPtrType {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return typeRefersToFuncDecl(t.Elem(), seen)
	case reflect.Map:
		return typeRefersToFuncDecl(t.Key(), seen) || typeRefersToFuncDecl(t.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if typeRefersToFuncDecl(t.Field(i).Type, seen) {
				return true
			}
		}
	}
	return false
}
