package semantic

import (
	"maps"
	"reflect"
	"sort"
	"strings"
	"sync"

	"elisacore/src/ast"
)

// collectRegionBackedPackedEnums returns the set of packed enums that are allocated via `new[auto]`
// somewhere in the program. ONLY these are region-backed (implicit-store) enums. An enum used purely
// with explicit stores (`new[store]`, `match in store`, frozen `.Store`) is never new[auto]-built,
// so it must NOT receive implicit store threading — that would break the legacy explicit-store code
// (e.g. the JSON parser's JsonNode). This is the precise gate on implicit packed-store injection.
func (a *Analyzer) collectRegionBackedPackedEnums(funcs []*ast.FuncDecl) map[string]bool {
	out := a.storeBackedPlainEnums()
	for _, fn := range funcs {
		if fn != nil {
			walkStoreScanNodes(fn.Body, func(node any) {
				if alloc, ok := node.(*ast.AllocExpr); ok && alloc != nil && alloc.AutoRegion {
					if enumType, _, ok := a.packedAllocConstructorInfo(alloc.Value); ok && enumType != nil && enumType.Packed {
						out[enumType.Root().Name] = true
					}
				}
			})
		}
	}
	return out
}

func (a *Analyzer) storeBackedPlainEnums() map[string]bool {
	out := map[string]bool{}
	// Store-backed plain enums are region-backed by design, independent of whether any call site uses
	// `new[auto]` syntax — their bare constructors allocate into the region too.
	for _, t := range a.namedTypes {
		if et, ok := t.(*EnumType); ok && et != nil && et.StoreBackedPlain {
			out[et.Root().Name] = true // docs/77: region-backed by hierarchy root
		}
	}
	return out
}

// packedStoreImplicitParamName is the synthetic name of the hidden store parameter threaded into a
// function that exposes a region-backed packed enum in its signature (docs/74). It carries the
// implicit per-region PackedStoreState so the function can build (`new[auto]`) or match the bare
// handle without an explicit `Store` value written by hand.
func packedStoreImplicitParamName(enumName string) string {
	return "__packed_store_" + sanitizeImplicitTempBase(enumName)
}

// computeTransitiveStoreNeeds computes, for each region-poly candidate function, the full set of
// region-backed packed enums whose implicit store it must carry: its own signature enums PLUS, via a
// call-graph fixpoint, every store needed by a function it calls. This is what makes MUTUAL recursion
// work — a consumer `sumT(Tree)->i64` (no region of its own) calls `sumF(Forest)`, so it must receive
// Forest's store threaded in; a builder `forest()->Forest` calls `leaf()->Tree`, so it must thread
// Tree's store down. Entry points (which own a region) create the stores on demand and are excluded
// from injection by the caller; every other function in the chain receives them threaded.
func (a *Analyzer) computeTransitiveStoreNeeds(funcs []*ast.FuncDecl, regionBacked map[string]bool) (needs, signatureNeeds map[*FuncType]map[string]*EnumType) {
	needs = map[*FuncType]map[string]*EnumType{}
	signatureNeeds = map[*FuncType]map[string]*EnumType{}
	callees := map[*FuncType]map[*FuncType]bool{}
	// builds: the roots each function constructs nodes of, directly or through a callee. A worker
	// submitted to another thread with a captured store must build none (analyzer_submit_store_capture.go).
	// The value names the function that constructs the nodes (the diagnostic's witness).
	builds := map[*FuncType]map[string]string{}
	// All signatures in this pass read the same declaration shapes.
	fieldRoots := map[*StructType][]Type{}
	emptyTypeRoots := make(map[Type]bool)
	var packedSignatures packedStoreSignatureCache
	bareVariantNames := a.packedStoreBareVariantNames()
	var order []*FuncType
	sig := make(map[string]*EnumType)
	for _, fn := range funcs {
		ft := a.funcTypeForRegionPoly(fn)
		if ft == nil {
			continue
		}
		if _, seen := callees[ft]; !seen {
			callees[ft] = map[*FuncType]bool{}
			order = append(order, ft)
			collectPackedStoreSignature(ft, sig, fieldRoots, emptyTypeRoots, &packedSignatures)
			for name, et := range sig {
				// Pointer handles are self-contained for reads. Construction
				// can still require a store, which is recorded below.
				if !regionBacked[name] || et.HandleIsPointer() {
					delete(sig, name)
				}
			}
			if len(sig) != 0 {
				signatureNeeds[ft] = make(map[string]*EnumType, len(sig))
				needs[ft] = make(map[string]*EnumType, len(sig))
				maps.Copy(signatureNeeds[ft], sig)
				maps.Copy(needs[ft], sig)
			}
			clear(sig)
		}
		constructors, bareVariants := a.scanPackedStoreBody(fn, callees[ft], bareVariantNames, nil)
		for name, et := range constructors {
			if regionBacked[name] && et.HandleIsPointer() {
				if needs[ft] == nil {
					needs[ft] = make(map[string]*EnumType)
				}
				needs[ft][name] = et
			}
			if regionBacked[name] {
				if builds[ft] == nil {
					builds[ft] = map[string]string{}
				}
				builds[ft][name] = fn.Name
			}
		}
		for name := range bareVariants {
			if regionBacked[name] {
				if builds[ft] == nil {
					builds[ft] = map[string]string{}
				}
				builds[ft][name] = fn.Name
			}
		}
	}
	propagatePackedStoreNeeds(order, callees, needs, builds)
	a.packedStoreBuilders = builds
	return needs, signatureNeeds
}

// injectStoreNeeds threads an implicit packed-store parameter for each region-backed enum in `needs`
// (signature ∪ transitive callee needs). Idempotent; skips enums already carried explicitly.
func (a *Analyzer) injectStoreNeeds(fnType *FuncType, needs map[string]*EnumType) {
	if fnType == nil || len(needs) == 0 {
		return
	}
	names := make([]string, 0, len(needs))
	for name := range needs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, enumName := range names {
		enumType := needs[enumName]
		if enumType == nil || enumType.StoreType == nil {
			continue
		}
		paramName := packedStoreImplicitParamName(enumName)
		if funcTypeHasImplicitParam(fnType, paramName) || funcTypeHasExplicitPackedStoreParam(fnType, enumName) {
			continue
		}
		storeType := PackedEnumStoreWithState(enumType.StoreType, a.namedTypes["Local"])
		fnType.Params = append(fnType.Params, storeType)
		fnType.ImplicitParamNames = append(fnType.ImplicitParamNames, paramName)
	}
}

// funcOwnsRegion reports whether a function establishes its own region with an `in auto:`/`in <arena>:`
// block (an ast.RegionStmt). Such a function CREATES region-backed stores on demand inside that region
// — possibly several, one per region in a loop (binary-trees builds a fresh tree per iteration) — so
// it must NOT receive a single threaded store for enums beyond its signature; that would force every
// per-region tree into one store and defeat region-local allocation.
func (a *Analyzer) funcOwnsRegion(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	found := false
	seen := borrowStoreScanSeen()
	defer releaseStoreScanSeen(seen)
	visit := func(node any) bool {
		if found {
			return false
		}
		// These leaves cannot contain a region scope or a shared subtree.
		switch node.(type) {
		case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
			*ast.CharLit, *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
			return false
		}
		key := reflect.ValueOf(node).Pointer()
		if seen.values[key] {
			return false
		}
		seen.values[key] = true
		switch n := node.(type) {
		case *ast.RegionStmt:
			found = true
		case *ast.InStoreStmt:
			// The parser uses this node for both arena and packed-store scopes.
			found = a.inStoreScopesArena(n.Store, fn)
		}
		return !found
	}
	for _, stmt := range fn.Body {
		walkPruned(stmt, visit)
		if found {
			break
		}
	}
	return found
}

// inStoreScopesArena reports whether an `in <store>:` scope targets an ARENA (a region-establishing
// scope) rather than a packed-store value. It uses the resolved type when available, falling back to a
// syntactic match against the function's Arena parameters — this classification can run before the
// body's expression types are populated, and the reported bug case (`in a:` over an Arena& param) must
// be recognized regardless.
func (a *Analyzer) inStoreScopesArena(store ast.Expr, fn *ast.FuncDecl) bool {
	if a == nil || store == nil {
		return false
	}
	s := stripParenExpr(store)
	if t := a.exprType(s); t != nil {
		return IsArenaValueOrRefType(t)
	}
	if id, ok := s.(*ast.Ident); ok && fn != nil {
		for _, p := range fn.Params {
			if p.Name == id.Name {
				return typeExprNamesArena(p.Type)
			}
		}
	}
	return false
}

// typeExprNamesArena reports whether a syntactic type expression names `Arena` anywhere (e.g. through a
// `mutable Arena&` reference wrapper). Reflection-walked so it is robust to the exact ref/wrapper node.
func typeExprNamesArena(t ast.TypeExpr) bool {
	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if nt, ok := v.Interface().(*ast.NamedType); ok {
				if nt != nil && nt.Name == "Arena" {
					found = true
				}
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(t))
	return found
}

// funcIsRegionStoreEntryPoint reports whether a function is a region-store entry point: a `@test`,
// `@bench`, or `main`. These own a region (an `in auto:` scope) and CREATE region-backed stores on
// demand, threading them into callees — so they must NOT receive store params themselves (the runtime
// calls them with no such argument). Every other function receives its needed stores threaded in.
func funcIsRegionStoreEntryPoint(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	return fn.Name == "main" || funcHasAnnotation(fn, "test") || funcHasAnnotation(fn, "bench")
}

// funcTypeHasExplicitPackedStoreParam reports whether an explicit parameter is already an `E.Store`
// (any state). When true the function threads its store by hand — the legacy explicit style — and
// must not also receive an implicit store.
func funcTypeHasExplicitPackedStoreParam(fnType *FuncType, enumName string) bool {
	if fnType == nil {
		return false
	}
	explicitCount := funcTypeExplicitParamCount(fnType)
	for i := 0; i < explicitCount && i < len(fnType.Params); i++ {
		if storeType, ok := StripAggregateStateType(fnType.Params[i]).(*PackedEnumStoreType); ok && storeType != nil && storeType.Enum != nil && storeType.Enum.Root().Name == enumName {
			return true
		}
	}
	return false
}

// defineImplicitPackedStoreParamSymbols binds each implicit `__packed_store_E` parameter as a local
// symbol in the function body and registers it as the active store for its enum, so `new[auto] E.V`
// and storeless `match` resolve to the threaded store, and a recursive call threads it onward.
func (a *Analyzer) defineImplicitPackedStoreParamSymbols(fn *ast.FuncDecl, fnType *FuncType) {
	if fnType == nil {
		return
	}
	explicitCount := funcTypeExplicitParamCount(fnType)
	for i, name := range fnType.ImplicitParamNames {
		if !strings.HasPrefix(name, "__packed_store_") {
			continue
		}
		idx := explicitCount + i
		if idx >= len(fnType.Params) {
			continue
		}
		storeType, ok := fnType.Params[idx].(*PackedEnumStoreType)
		if !ok || storeType == nil {
			continue
		}
		if _, exists := a.currentScope.Lookup(name); !exists {
			sym := &Symbol{Name: name, Kind: SymbolParam, Type: storeType, Node: fn, ParamIndex: idx}
			a.defineLocal(sym, fn.Pos())
		}
		a.bindActivePackedStoreType(storeType)
	}
}

// PackedStoreImplicitUsePrefix marks a synthetic store argument for a CONSUMER call — the callee
// receives existing handles of the enum through its explicit params, so the backend must resolve
// the argument to the store those handles were built against (the active binding), never by
// creating a fresh region-local store (an empty store makes every handle read undefined).
const PackedStoreImplicitUsePrefix = "__packed_store_use_"

// packedStoreImplicitArgExpr is the synthetic argument threaded for an implicit packed store param.
// In a caller that already has the store (its own implicit param), the same-name lookup resolves it;
// otherwise the backend resolves it on demand at this call site: a pure builder call (enum only in
// the callee's return type) may create the region-backed store fresh, while a consumer call (enum
// handles flow IN through explicit params) is marked with PackedStoreImplicitUsePrefix so the
// backend reuses the binding the handles came from.
func packedStoreImplicitArgExpr(storeType *PackedEnumStoreType, consume bool) ast.Expr {
	if storeType == nil || storeType.Enum == nil {
		return &ast.Ident{Name: "__packed_store_"}
	}
	rootName := storeType.Enum.Root().Name
	if consume {
		return &ast.Ident{Name: PackedStoreImplicitUsePrefix + sanitizeImplicitTempBase(rootName)}
	}
	return &ast.Ident{Name: packedStoreImplicitParamName(rootName)}
}

// FuncTypeConsumesPackedEnumRoot reports whether an explicit parameter of ft (directly or through a
// struct/container, same walk as injection) carries handles of the packed enum hierarchy rooted at
// rootName — i.e. whether a call to ft consumes pre-existing handles rather than only building new ones.
func FuncTypeConsumesPackedEnumRoot(ft *FuncType, rootName string) bool {
	if ft == nil || rootName == "" {
		return false
	}
	enums := map[string]*EnumType{}
	collectPackedEnumsInSignature(ft, false, enums)
	_, ok := enums[rootName]
	return ok
}

// collectPackedEnumsInType records the packed enums appearing in a type, canonicalized to their
// hierarchy ROOT (docs/77) — a sealed hierarchy shares ONE store per root, so threading keys, param
// names, and store types are all the root's. For a plain enum the root is itself.
// Struct fields and darray/slice elements are walked recursively so that a function whose signature
// mentions a struct or container holding packed-enum handles (e.g. a parse-result struct wrapping a
// darray[Stmt]) also receives the implicit store threading it needs.
// Type graphs are walked repeatedly while checking calls. Reuse the traversal
// map rather than rebuilding its buckets for every signature. Maps are private
// to one walk; clear keys before pooling so no AST/type graph is retained.
var packedTypeSeenPool = sync.Pool{New: func() any { return make(map[Type]int, 32) }}

func collectPackedEnumsInSignature(ft *FuncType, includeReturn bool, out map[string]*EnumType) {
	collectPackedEnumsInSignatureWithOrder(ft, includeReturn, out, nil, nil)
}

type packedTypeWalk struct {
	seen       map[Type]int
	fieldRoots map[*StructType][]Type
}

func collectPackedEnumsInSignatureWithOrder(ft *FuncType, includeReturn bool, out map[string]*EnumType, fieldRoots map[*StructType][]Type, emptyTypeRoots map[Type]bool) {
	walk := packedTypeWalk{fieldRoots: fieldRoots}
	explicitCount := min(funcTypeExplicitParamCount(ft), len(ft.Params))
	for i := 0; i < explicitCount; i++ {
		walk.collectSignatureRoot(ft.Params[i], out, emptyTypeRoots)
	}
	if includeReturn {
		walk.collectSignatureRoot(ft.Return, out, emptyTypeRoots)
	}
	if walk.seen != nil {
		releasePackedTypeSeen(walk.seen)
	}
}

func packedStoreSignatureRoot(t Type) Type {
	t = StripAggregateStateType(t)
	switch t.(type) {
	case *RefType, *OptionalType, *ErrorUnionType, *EnumType, *PackedVariantViewType, *DArrayType, *StructType, *TupleType:
		return t
	default:
		return nil
	}
}

// Only cache an empty root when it was walked without earlier roots
// pruning its graph. Nonempty roots still share traversal state within
// the signature so depth limits and first-witness selection are unchanged.
func (walk *packedTypeWalk) collectSignatureRoot(t Type, out map[string]*EnumType, emptyTypeRoots map[Type]bool) {
	t = packedStoreSignatureRoot(t)
	if t == nil {
		return
	}
	if emptyTypeRoots[t] {
		return
	}
	// Scalar-only and already-classified signatures need no traversal map.
	if walk.seen == nil {
		walk.seen = packedTypeSeenPool.Get().(map[Type]int)
	}
	isolated := emptyTypeRoots != nil && len(walk.seen) == 0 && len(out) == 0
	walk.collect(t, out, 0)
	if isolated && len(out) == 0 {
		emptyTypeRoots[t] = true
		if len(walk.seen) <= packedTypeSeenKeepLimit {
			clear(walk.seen)
		}
	}
}

func collectPackedEnumsInType(t Type, out map[string]*EnumType) {
	walk := packedTypeWalk{seen: packedTypeSeenPool.Get().(map[Type]int)}
	walk.collect(t, out, 0)
	releasePackedTypeSeen(walk.seen)
}

const packedTypeSeenKeepLimit = 4096

func releasePackedTypeSeen(seen map[Type]int) {
	if len(seen) <= packedTypeSeenKeepLimit {
		clear(seen)
		packedTypeSeenPool.Put(seen)
	}
}

const collectPackedEnumsMaxDepth = 8 // guard against infinite recursion in recursive types

func (walk packedTypeWalk) collect(t Type, out map[string]*EnumType, depth int) {
	if depth > collectPackedEnumsMaxDepth {
		return
	}
	t = StripAggregateStateType(t)
	switch t.(type) {
	case *RefType, *OptionalType, *ErrorUnionType, *EnumType, *PackedVariantViewType, *DArrayType, *StructType, *TupleType:
	default:
		return
	}
	// Shared type graphs are DAGs as well as cycles. A revisit at an equal or
	// greater depth cannot expose another root. Keep the shallowest depth so a
	// later shorter path still gets the full remaining traversal budget.
	if previousDepth, visited := walk.seen[t]; visited && previousDepth <= depth {
		return
	}
	walk.seen[t] = depth
	switch tt := t.(type) {
	case *RefType:
		walk.collect(tt.Elem, out, depth+1)
	case *OptionalType:
		walk.collect(tt.Value, out, depth+1)
	case *ErrorUnionType:
		walk.collect(tt.Value, out, depth+1)
	case *EnumType:
		if tt == nil {
			break
		}
		if tt.Packed {
			root := tt.Root()
			if _, already := out[root.Name]; already {
				// Already collected this root — its payloads were (or are being) walked. Stop to
				// avoid re-walking a recursive payload (`Ty.Ptr(inner: Ty)`) past the depth guard.
				break
			}
			out[root.Name] = root
		}
		// Recurse into variant payloads — for a PACKED enum too. A payload may be a packed enum of a
		// DIFFERENT root (`Decl.VarDecl` carries a `Type` handle, `Stmt.Compound` a `Block` holding
		// `darray[Decl]`/`darray[Stmt]`): destructuring the outer handle yields the inner handle,
		// whose `match` needs ITS root's store threaded in. Without this, a function that receives a
		// `Decl` and `expect`s its `type_expr` as a `Type.Name(...)` fails with "match over Type
		// requires an in Type.Store clause", because only Decl's store was collected. Hierarchy
		// children are walked so a root/category param exposes every reachable leaf's payloads.
		for _, variant := range tt.Variants {
			if variant == nil {
				continue
			}
			for _, payload := range variant.Payload {
				walk.collect(payload, out, depth+1)
			}
		}
		for _, child := range tt.Children {
			walk.collect(child, out, depth+1)
		}
	case *PackedVariantViewType:
		if tt != nil && tt.Enum != nil && tt.Enum.Packed {
			root := tt.Enum.Root()
			out[root.Name] = root
		}
	case *DArrayType:
		if tt != nil {
			walk.collect(tt.Elem, out, depth+1)
		}
	case *StructType:
		if tt != nil {
			// Preserve field order for first-witness selection. Signature
			// collection shares a cache while declaration shapes are fixed.
			if walk.fieldRoots == nil {
				for _, name := range orderedStructFieldNames(tt) {
					walk.collect(tt.Fields[name].Type, out, depth+1)
				}
				return
			}
			roots, cached := walk.fieldRoots[tt]
			if !cached {
				for _, name := range orderedStructFieldNames(tt) {
					if root := packedStoreSignatureRoot(tt.Fields[name].Type); root != nil {
						roots = append(roots, root)
					}
				}
				walk.fieldRoots[tt] = roots
			}
			for _, root := range roots {
				walk.collect(root, out, depth+1)
			}
		}
	case *TupleType:
		if tt != nil {
			for _, field := range tt.Fields {
				walk.collect(field.Type, out, depth+1)
			}
		}
	}
}
