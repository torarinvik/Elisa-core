package semantic

import (
	"reflect"
	"strings"
	"sync"
	"unsafe"

	"elisacore/src/ast"
)

// sharedCodegen lets several code generators read one Result concurrently.
// They read its maps without locks: nothing writes them while the session is
// open. The analyzer behind the Result is used only under mu. A
// specialization's re-analysis (SpecializedExprTypes) rewrites expression
// types temporarily, so for the session the analyzer works on a private copy
// of the expression-type map, never on the one Result.ExprTypes shares with
// the generators. Each instantiation is re-analyzed once and memoized.
type sharedCodegen struct {
	mu   sync.Mutex
	memo map[specializationKey]map[ast.Expr]Type
	// restore puts back the analyzer's own maps when the session ends.
	restore []func()
}

type specializationKey struct {
	fn   *ast.FuncDecl
	args string
}

// BeginSharedCodegen prepares r for concurrent code generators. The caller
// must call EndSharedCodegen after every generator has finished, and nothing
// else may use r in between.
func (r *Result) BeginSharedCodegen() {
	sh := &sharedCodegen{memo: map[specializationKey]map[ast.Expr]Type{}}
	if a := r.analyzer; a != nil {
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
	sh.mu.Lock()
	return sh.mu.Unlock
}

func (sh *sharedCodegen) specializedExprTypes(r *Result, fn *ast.FuncDecl, typeArgs []Type) map[ast.Expr]Type {
	key, keyed := specializationMemoKey(fn, typeArgs)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if cached, ok := sh.memo[key]; keyed && ok {
		return cached
	}
	overlay := r.specializedExprTypes(fn, typeArgs)
	if keyed {
		sh.memo[key] = overlay
	}
	return overlay
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
