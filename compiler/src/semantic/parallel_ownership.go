package semantic

import (
	"reflect"
	"sync"
	"sync/atomic"

	"elisacore/src/ast"
)

// parallelWorkerState is a worker's view of declaration ownership during parallel
// body analysis. A worker writes only into the bodies it owns; anything it must
// analyze on another declaration's behalf (a quiet provisional-summary pass) runs
// on a private copy taken from the immutable snapshot made at fork time.
type parallelWorkerState struct {
	worker                         int
	snapshots                      []*ast.SyntaxSnapshot // per body declaration, taken at fork time
	frozen                         []ast.Decl            // pristine reflective copies where a snapshot declined
	predicateMemoBases             parallelPredicateMemoBases
	programLivedTypesBase          map[Type]bool
	smtLocalDefDepsBase            map[*Symbol][]string
	resolvedTypeNamesBase          map[ast.TypeExpr]string
	abstractParamRegionRefMemoBase map[abstractParamRegionRefKey]regionRefState
	abstractRefRestartMemoBase     map[abstractRefRestartKey]bool
	// at locates a body declaration in the snapshot: top-level index, and member
	// index for an impl method (-1 for a top-level function).
	at map[*ast.FuncDecl][2]int
	// slots holds each body's owning worker (-1 until a worker claims it).
	slots       []atomic.Int32
	shadows     map[*ast.FuncDecl]*ast.FuncDecl
	readShadows map[*ast.FuncDecl]*ast.FuncDecl
	// funcTypes maps a shared FuncType to the private copy this worker's analyses
	// write function facts into; owned copies are written back after the merge.
	funcTypes     map[*FuncType]*FuncType
	funcTypeOwned map[*FuncType]bool
	// Sources identifies already-private types, including derived signatures
	// whose nil source means they have no declaration to publish back into.
	funcTypeSources map[*FuncType]*FuncType
	// Unit publication mutates completed shared FuncType summaries under the
	// scheduler mutex. Lazy whole-type copies must use the same read boundary.
	// Body analysis and worker-local summary inference never hold this mutex.
	funcTypeMu *sync.Mutex
	original   map[*ast.FuncDecl]*ast.FuncDecl
	// imported marks foreign declarations whose finished facts this worker holds.
	imported      map[*ast.FuncDecl]bool
	importedUnits map[int]bool
	// smtFlights shares solver verdicts between workers: the first worker to ask a
	// query solves it and the others wait for its verdict.
	smtFlights *sync.Map
	leading    map[string]*smtFlight
}

type parallelPredicateMemoBases struct {
	affineHandles             map[Type]bool
	borrowedOwnerRefs         map[Type]bool
	regionRefs                map[Type]bool
	mutableBorrowCapabilities map[Type]bool
	storeScanAddresses        map[Type]bool
	storeScanPlainValues      map[Type]bool
	storeScanProgramLived     map[Type]bool
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
	owner, known := p.ownerOf(decl)
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
	owner, known := p.ownerOf(decl)
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
	if original, isShadow := p.original[decl]; isShadow {
		// Cached graph slices can retain a shadow made before the owner's
		// completed unit was imported. Resolve both forms to the finished
		// declaration so summary/SCC keys cannot split into two identities.
		if p.imported[original] {
			return original
		}
		return decl
	}
	owner, known := p.ownerOf(decl)
	if !known || owner == p.worker || p.imported[decl] {
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

// Body views have private syntax, but direct recursion and proof guards must
// retain declaration identity. Otherwise a shadow's self-call would resolve to
// its original declaration and could incorrectly appear non-recursive.
func (a *Analyzer) canonicalFuncDecl(decl *ast.FuncDecl) *ast.FuncDecl {
	if a != nil && a.parallel != nil {
		if original, ok := a.parallel.original[decl]; ok {
			return original
		}
	}
	return decl
}

// parallelReadDeclSlice maps canonical graph entries through this worker's read
// shadows. Warm caches are built before workers exist, so their identities are
// canonical; a worker must use its own shadow identities when traversing a
// foreign declaration to keep SCC and summary keys consistent.
func (a *Analyzer) parallelReadDeclSlice(decls []*ast.FuncDecl) ([]*ast.FuncDecl, bool) {
	if a == nil || a.parallel == nil || len(decls) == 0 {
		return decls, false
	}
	var mapped []*ast.FuncDecl
	for i, decl := range decls {
		resolved := a.readDecl(decl)
		if resolved == decl {
			continue
		}
		if mapped == nil {
			mapped = make([]*ast.FuncDecl, len(decls))
			copy(mapped, decls)
		}
		mapped[i] = resolved
	}
	return mapped, mapped != nil
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

// privateFuncType gives a parallel worker's analysis of fn a private copy of its
// shared FuncType, so publishing the function's facts (return provenance, effects,
// isolation) never races with other workers reading the shared one. Readers keep
// seeing the pre-fork facts until the merge writes the owner's copy back.
func (a *Analyzer) privateFuncType(fn *ast.FuncDecl, shared *FuncType) *FuncType {
	p := a.parallel
	if p == nil || shared == nil {
		return shared
	}
	if source, private := p.funcTypeSources[shared]; private {
		if source != nil {
			p.maybeOwnFuncType(fn, source)
		}
		return shared
	}
	if private := p.funcTypes[shared]; private != nil {
		p.maybeOwnFuncType(fn, shared)
		return private
	}
	private := new(FuncType)
	if p.funcTypeMu != nil {
		p.funcTypeMu.Lock()
	}
	settable(reflect.ValueOf(private).Elem()).Set(snapshotFuncTypeForDestination(shared, private))
	if p.funcTypeMu != nil {
		p.funcTypeMu.Unlock()
	}
	if p.funcTypes == nil {
		p.funcTypes = map[*FuncType]*FuncType{}
	}
	p.funcTypes[shared] = private
	p.recordPrivateFuncType(private, shared)
	p.maybeOwnFuncType(fn, shared)
	return private
}

func (p *parallelWorkerState) recordPrivateFuncType(private, source *FuncType) {
	if p == nil || private == nil {
		return
	}
	if p.funcTypeSources == nil {
		p.funcTypeSources = map[*FuncType]*FuncType{}
	}
	p.funcTypeSources[private] = source
}

// A quiet scan can cache provisional foreign facts before a later unit imports
// the finished callee. Evict that version at the import boundary: the next query
// must read the newly published summary. Do not mutate the old private object,
// which may already be referenced by previously exported node facts.
func (a *Analyzer) invalidateImportedFuncType(fn *ast.FuncDecl) {
	p := a.parallel
	if p == nil {
		return
	}
	if sym, ok := a.symbolForFuncDecl(fn); ok && sym != nil {
		if shared, ok := sym.Type.(*FuncType); ok && !p.funcTypeOwned[shared] {
			delete(p.funcTypes, shared)
		}
	}
}

// maybeOwnFuncType promotes a cached worker-local type when its declaration is
// claimed later. Calls made before the body is claimed still need a private copy,
// but must not prevent the eventual owner from publishing its completed facts.
func (p *parallelWorkerState) maybeOwnFuncType(fn *ast.FuncDecl, shared *FuncType) {
	if p == nil || fn == nil {
		return
	}
	decl := fn
	if orig, isShadow := p.original[fn]; isShadow {
		decl = orig
		// A shadow is a foreign read/analysis view and must never publish facts.
		return
	}
	if owner, known := p.ownerOf(decl); known && owner == p.worker {
		if p.funcTypeOwned == nil {
			p.funcTypeOwned = map[*FuncType]bool{}
		}
		p.funcTypeOwned[shared] = true
	}
}

// publishFuncTypes writes a worker's owned function facts into the shared types.
func (p *parallelWorkerState) publishFuncTypes() {
	for shared, private := range p.funcTypes {
		if p.funcTypeOwned[shared] {
			settable(reflect.ValueOf(shared).Elem()).Set(snapshotFuncTypeForDestination(private, shared))
		}
	}
}

func (p *parallelWorkerState) importedUnit(u int) bool {
	if p.importedUnits == nil {
		p.importedUnits = map[int]bool{}
	}
	if p.importedUnits[u] {
		return true
	}
	p.importedUnits[u] = true
	return false
}

// ownerOf returns the worker that owns a body declaration; known reports whether
// decl is a body at all. An unclaimed body (-1) is foreign to every worker.
func (p *parallelWorkerState) ownerOf(decl *ast.FuncDecl) (int, bool) {
	loc, ok := p.at[decl]
	if !ok {
		return 0, false
	}
	return int(p.slots[loc[0]].Load()), true
}

type smtFlight struct {
	done   chan struct{}
	result smtQueryResult
	ok     bool
}

// awaitSharedSMTQuery returns another worker's verdict for query, waiting while it
// is being solved. On a miss the caller becomes the query's leader and must
// resolve or release it.
func (a *Analyzer) awaitSharedSMTQuery(query string) (smtQueryResult, bool) {
	p := a.parallel
	if p == nil || p.smtFlights == nil {
		return smtQueryResult{}, false
	}
	if p.leading[query] != nil {
		return smtQueryResult{}, false
	}
	fresh := &smtFlight{done: make(chan struct{})}
	if v, loaded := p.smtFlights.LoadOrStore(query, fresh); loaded {
		f := v.(*smtFlight)
		<-f.done
		return f.result, f.ok
	}
	if p.leading == nil {
		p.leading = map[string]*smtFlight{}
	}
	p.leading[query] = fresh
	return smtQueryResult{}, false
}

func (a *Analyzer) resolveSMTFlight(query string, result smtQueryResult) {
	if p := a.parallel; p != nil {
		if f := p.leading[query]; f != nil {
			delete(p.leading, query)
			f.result, f.ok = result, true
			close(f.done)
		}
	}
}

// releaseSMTFlight ends a leadership that produced no stable verdict, so waiters
// solve for themselves and later askers start a new flight.
func (a *Analyzer) releaseSMTFlight(query string) {
	if p := a.parallel; p != nil {
		if f := p.leading[query]; f != nil {
			delete(p.leading, query)
			p.smtFlights.CompareAndDelete(query, f)
			close(f.done)
		}
	}
}
