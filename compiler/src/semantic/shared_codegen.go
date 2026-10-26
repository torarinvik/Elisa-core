package semantic

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
	"unsafe"

	"elisacore/src/ast"
)

// sharedCodegen lets several code generators read one Result concurrently.
// They read its maps without locks: nothing writes them while the session is
// open. Analyzer-backed queries use analyzerMu, while specialization products
// and their lazy canonical indexes use independent synchronization. A
// specialization's re-analysis owns private syntax and a private semantic view;
// each resulting immutable product is memoized once per instantiation. Other
// analyzer-backed codegen queries use detached public maps for this session.
type sharedCodegen struct {
	analyzerMu                 sync.Mutex
	memoMu                     sync.Mutex
	unkeyedMu                  sync.Mutex
	canonicalMu                sync.Mutex
	bodies                     map[specializationKey]*SpecializedFunction
	inFlight                   map[specializationKey]*specializationCall
	compilerBuiltinHelperCalls map[*ast.CallExpr]string
	programRegions             map[string]bool
	canonicalNodes             map[ast.Node]bool
	canonicalSymbols           map[*Symbol]bool
	canonicalScopes            map[*Scope]bool
	// restore puts back the analyzer's own maps when the session ends.
	restore []func()
}

type specializationCall struct {
	done    chan struct{}
	product *SpecializedFunction
	err     error
}

type specializationKey struct {
	fn   *ast.FuncDecl
	args string
}

// BeginSharedCodegen prepares r for concurrent code generators. The caller
// must call EndSharedCodegen after every generator has finished, and nothing
// else may use r in between.
func (r *Result) BeginSharedCodegen() {
	sh := &sharedCodegen{bodies: map[specializationKey]*SpecializedFunction{}}
	if a := r.analyzer; a != nil {
		// These codegen queries are frequent and immutable after analysis. Give
		// the concurrent workers detached snapshots so they do not serialize on
		// the analyzer mutex for every call and region-name lookup.
		sh.compilerBuiltinHelperCalls = maps.Clone(a.compilerBuiltinHelperCalls)
		sh.programRegions = maps.Clone(a.programRegions)
		sh.restore = privatizeSharedMaps(r, a)
	}
	r.shared = sh
}

// EndSharedCodegen restores the analyzer's map and drops the memo: a later
// update may change the bodies.
func (r *Result) EndSharedCodegen() {
	if sh := r.shared; sh != nil {
		for _, restore := range sh.restore {
			restore()
		}
	}
	r.shared = nil
}

// lockAnalyzer serializes use of the analyzer during a shared session; it
// returns the unlock function (a no-op outside one).
func (r *Result) lockAnalyzer() func() {
	sh := r.shared
	if sh == nil {
		return func() {}
	}
	sh.analyzerMu.Lock()
	return sh.analyzerMu.Unlock
}

// specialize memoizes successful work per key. Different instantiations can
// analyze concurrently, while callers for the same instantiation share one
// in-flight product.
func (sh *sharedCodegen) specialize(key specializationKey, build func() (*SpecializedFunction, error)) (*SpecializedFunction, error) {
	sh.memoMu.Lock()
	if sh.bodies == nil {
		sh.bodies = make(map[specializationKey]*SpecializedFunction)
	}
	if product, ok := sh.bodies[key]; ok {
		sh.memoMu.Unlock()
		return product, nil
	}
	if call := sh.inFlight[key]; call != nil {
		sh.memoMu.Unlock()
		<-call.done
		return call.product, call.err
	}
	if sh.inFlight == nil {
		sh.inFlight = make(map[specializationKey]*specializationCall)
	}
	call := &specializationCall{done: make(chan struct{})}
	sh.inFlight[key] = call
	sh.memoMu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			sh.memoMu.Lock()
			if sh.inFlight[key] == call {
				call.err = fmt.Errorf("specialization construction panicked: %v", recovered)
				delete(sh.inFlight, key)
				close(call.done)
			}
			sh.memoMu.Unlock()
			panic(recovered)
		}
	}()

	product, err := build()

	sh.memoMu.Lock()
	defer sh.memoMu.Unlock()
	if err == nil {
		sh.bodies[key] = product
	}
	call.product = product
	call.err = err
	delete(sh.inFlight, key)
	close(call.done)
	return product, err
}

func specializationMemoKey(fn *ast.FuncDecl, typeArgs []Type) (specializationKey, bool) {
	var b strings.Builder
	for _, t := range typeArgs {
		k, ok := canonicalTypeIDKey(t)
		if !ok {
			return specializationKey{}, false
		}
		b.WriteString(k)
		b.WriteByte(0)
	}
	return specializationKey{fn: fn, args: b.String()}, true
}

// privatizeSharedMaps gives the analyzer a private copy of every map it shares
// with r, so re-analysis during the session never writes a map the
// generators read. It returns the functions that put the originals back.
func privatizeSharedMaps(r *Result, a *Analyzer) []func() {
	shared := map[uintptr]bool{}
	rv := reflect.ValueOf(r).Elem()
	for i := 0; i < rv.NumField(); i++ {
		if f := rv.Field(i); f.Kind() == reflect.Map && !f.IsNil() {
			shared[f.Pointer()] = true
		}
	}
	var restore []func()
	av := reflect.ValueOf(a).Elem()
	for i := 0; i < av.NumField(); i++ {
		f := av.Field(i)
		if f.Kind() != reflect.Map || f.IsNil() || !shared[f.Pointer()] {
			continue
		}
		field := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
		original := reflect.ValueOf(field.Interface())
		clone := reflect.MakeMapWithSize(f.Type(), original.Len())
		iter := original.MapRange()
		// Key/Value allocate fresh reflected values for every entry. Reuse
		// scratch storage instead; SetMapIndex copies each entry into clone.
		key := reflect.New(f.Type().Key()).Elem()
		value := reflect.New(f.Type().Elem()).Elem()
		for iter.Next() {
			key.SetIterKey(iter)
			value.SetIterValue(iter)
			clone.SetMapIndex(key, value)
		}
		field.Set(clone)
		restore = append(restore, func() { field.Set(original) })
	}
	return restore
}
