package semantic

import (
	"reflect"
	"sort"
	"strings"

	"elisacore/src/ast"
)

// collectRegionBackedPackedEnums returns the set of packed enums that are allocated via `new[auto]`
// somewhere in the program. ONLY these are region-backed (implicit-store) enums. An enum used purely
// with explicit stores (`new[store]`, `match in store`, frozen `.Store`) is never new[auto]-built,
// so it must NOT receive implicit store threading — that would break the legacy explicit-store code
// (e.g. the JSON parser's JsonNode). This is the precise gate on implicit packed-store injection.
func (a *Analyzer) collectRegionBackedPackedEnums(funcs []*ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	// docs/76: a recursive plain enum is region-backed by design (the default), independent of whether
	// any call site uses `new[auto]` syntax — its bare constructors allocate into the region too.
	for _, t := range a.namedTypes {
		if et, ok := t.(*EnumType); ok && et != nil && et.RecursivePlain {
			out[et.Root().Name] = true // docs/77: region-backed by hierarchy root
		}
	}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if alloc, ok := v.Interface().(*ast.AllocExpr); ok && alloc != nil && alloc.AutoRegion {
				if enumType, _, ok := a.packedAllocConstructorInfo(alloc.Value); ok && enumType != nil && enumType.Packed {
					out[enumType.Root().Name] = true
				}
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
	for _, fn := range funcs {
		if fn != nil {
			rec(reflect.ValueOf(fn.Body))
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

// injectInferredPackedStoreParams threads an implicit `E.Store[Local]` parameter into a function that
// exposes a packed enum E in its signature (an explicit parameter or its return type). A bare packed
// handle is a u32 index that is only meaningful against one PackedStoreState, so any function that
// builds or consumes such a handle must share that store — this injects it as a hidden param,
// threaded at call sites (the root call creates the region-backed store; the rest reuse it).
//
// It deliberately does NOT fire when the function already threads an EXPLICIT `E.Store` param: that
// is the legacy explicit-store style, which must keep working unchanged. Idempotent.
func (a *Analyzer) injectInferredPackedStoreParams(fnType *FuncType, regionBacked map[string]bool) {
	if fnType == nil || len(regionBacked) == 0 {
		return
	}
	enums := map[string]*EnumType{}
	explicitCount := funcTypeExplicitParamCount(fnType)
	for i := 0; i < explicitCount && i < len(fnType.Params); i++ {
		collectPackedEnumsInType(fnType.Params[i], enums)
	}
	collectPackedEnumsInType(fnType.Return, enums)
	if len(enums) == 0 {
		return
	}
	names := make([]string, 0, len(enums))
	for name := range enums {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, enumName := range names {
		if !regionBacked[enumName] {
			continue // explicit-store enum — never thread an implicit store (would regress legacy code)
		}
		enumType := enums[enumName]
		if enumType == nil || enumType.StoreType == nil {
			continue
		}
		if enumType.HandleIsPointer() {
			continue // docs/82: ptr handles are self-contained; no boundary store needed
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

// funcConstructsPackedEnumRoots returns the hierarchy roots (by name) of every region-backed packed
// enum the function CONSTRUCTS in its body — `new`/`new[auto]` constructor allocations plus bare
// constructors of recursive-plain enums (pre-rewrite CallExprs). docs/82: for `handle: ptr` enums
// this is the store-threading seed — reads are self-contained (the handle is the record address),
// so only construction pulls the store in.
func (a *Analyzer) funcConstructsPackedEnumRoots(fn *ast.FuncDecl) map[string]*EnumType {
	out := map[string]*EnumType{}
	if fn == nil {
		return out
	}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.AllocExpr:
				if n != nil {
					if enumType, _, ok := a.packedAllocConstructorInfo(n.Value); ok && enumType != nil && enumType.Packed {
						out[enumType.Root().Name] = enumType.Root()
					}
				}
			case *ast.CallExpr:
				if et, ok := a.regionBackedEnumConstructor(n); ok && et != nil {
					out[et.Root().Name] = et.Root()
				}
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
	rec(reflect.ValueOf(fn.Body))
	return out
}

// computeTransitiveStoreNeeds computes, for each region-poly candidate function, the full set of
// region-backed packed enums whose implicit store it must carry: its own signature enums PLUS, via a
// call-graph fixpoint, every store needed by a function it calls. This is what makes MUTUAL recursion
// work — a consumer `sumT(Tree)->i64` (no region of its own) calls `sumF(Forest)`, so it must receive
// Forest's store threaded in; a builder `forest()->Forest` calls `leaf()->Tree`, so it must thread
// Tree's store down. Entry points (which own a region) create the stores on demand and are excluded
// from injection by the caller; every other function in the chain receives them threaded.
func (a *Analyzer) computeTransitiveStoreNeeds(funcs []*ast.FuncDecl, regionBacked map[string]bool) map[*FuncType]map[string]*EnumType {
	needs := map[*FuncType]map[string]*EnumType{}
	callees := map[*FuncType]map[*FuncType]bool{}
	var order []*FuncType
	for _, fn := range funcs {
		ft := a.funcTypeForRegionPoly(fn)
		if ft == nil {
			continue
		}
		if _, seen := needs[ft]; !seen {
			needs[ft] = map[string]*EnumType{}
			callees[ft] = map[*FuncType]bool{}
			order = append(order, ft)
		}
		sig := map[string]*EnumType{}
		explicitCount := funcTypeExplicitParamCount(ft)
		for i := 0; i < explicitCount && i < len(ft.Params); i++ {
			collectPackedEnumsInType(ft.Params[i], sig)
		}
		collectPackedEnumsInType(ft.Return, sig)
		for name, et := range sig {
			// docs/82 `handle: ptr`: the handle is the record address, so signature exposure
			// alone needs no store — reads are self-contained. Construction still does (below).
			if regionBacked[name] && !et.HandleIsPointer() {
				needs[ft][name] = et
			}
		}
		for name, et := range a.funcConstructsPackedEnumRoots(fn) {
			if regionBacked[name] && et.HandleIsPointer() {
				needs[ft][name] = et
			}
		}
		// regionPolyFn supplies the generic-param protocol bounds so `B.method(...)`
		// dispatches contribute call-graph edges to every impl (union over impls —
		// the concrete target is specialization-dependent).
		a.regionPolyFn = fn
		a.collectCalleeFuncTypes(fn.Body, callees[ft])
		a.regionPolyFn = nil
	}
	for changed := true; changed; {
		changed = false
		for _, ft := range order {
			for g := range callees[ft] {
				for name, et := range needs[g] {
					if needs[ft][name] == nil {
						needs[ft][name] = et
						changed = true
					}
				}
			}
		}
	}
	return needs
}

// collectCalleeFuncTypes records the FuncTypes of every directly-called function in a body (resolving
// plain-identifier calls to their visible global). Used to build the call-graph edges for the
// transitive store-need fixpoint.
func (a *Analyzer) collectCalleeFuncTypes(body []ast.Stmt, out map[*FuncType]bool) {
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
				if ft := a.regionPolyCalleeFuncType(call); ft != nil {
					out[ft] = true
				}
				if fieldExpr, ok := unwrapParenForRegionPoly(call.Func).(*ast.FieldExpr); ok {
					for _, ft := range a.regionPolyProtocolMethodImplFuncTypes(fieldExpr) {
						out[ft] = true
					}
					// Extension-method callee: impl methods live in the per-receiver registry, not
					// the global namespace, so the name-based resolution above misses them. The
					// receiver type isn't analyzed yet in this pre-pass, so union over every
					// registered method with this name — the same conservative shape as the
					// protocol-impl union (extra edges only thread extra store params).
					for _, candidate := range a.visibleNameCandidates(fieldExpr.Field) {
						for _, method := range a.extensionMethodsByName[candidate] {
							if method == nil || method.Symbol == nil {
								continue
							}
							if ft, ok := method.Symbol.Type.(*FuncType); ok && ft != nil {
								out[ft] = true
							}
						}
					}
				}
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
	rec(reflect.ValueOf(body))
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
			if _, ok := v.Interface().(*ast.RegionStmt); ok {
				found = true
				return
			}
			// An `in <arena>:` scope is parsed as an InStoreStmt over an Arena value (NOT a RegionStmt),
			// yet it likewise establishes a region in which the function creates region-backed stores on
			// demand — e.g. `build_into(a: mutable Arena&)` doing `in a: root = make(N)`. Without treating
			// it as region ownership the function is judged region-LESS, so the caller hoists+threads the
			// store and the inner `in a:` can no longer re-home it: the store (and its columns) lands on a
			// synthesized __auto_ region in the caller instead of `a`. A packed-store `in <store>:` scope
			// is NOT arena-owning, so we gate on the scope target being an Arena.
			if in, ok := v.Interface().(*ast.InStoreStmt); ok && in != nil && a.inStoreScopesArena(in.Store, fn) {
				found = true
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
	rec(reflect.ValueOf(fn.Body))
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
	if t := a.exprTypes[s]; t != nil {
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
	explicitCount := funcTypeExplicitParamCount(ft)
	for i := 0; i < explicitCount && i < len(ft.Params); i++ {
		collectPackedEnumsInType(ft.Params[i], enums)
	}
	_, ok := enums[rootName]
	return ok
}

// collectPackedEnumsInType records the packed enums appearing in a type, canonicalized to their
// hierarchy ROOT (docs/77) — a sealed hierarchy shares ONE store per root, so threading keys, param
// names, and store types are all the root's. For a plain enum the root is itself.
// Struct fields and darray/slice elements are walked recursively so that a function whose signature
// mentions a struct or container holding packed-enum handles (e.g. a parse-result struct wrapping a
// darray[Stmt]) also receives the implicit store threading it needs.
func collectPackedEnumsInType(t Type, out map[string]*EnumType) {
	collectPackedEnumsInTypeDepth(t, out, 0)
}

const collectPackedEnumsMaxDepth = 8 // guard against infinite recursion in recursive types

func collectPackedEnumsInTypeDepth(t Type, out map[string]*EnumType, depth int) {
	if depth > collectPackedEnumsMaxDepth {
		return
	}
	switch tt := StripAggregateStateType(t).(type) {
	case *RefType:
		collectPackedEnumsInTypeDepth(tt.Elem, out, depth+1)
	case *OptionalType:
		collectPackedEnumsInTypeDepth(tt.Value, out, depth+1)
	case *ErrorUnionType:
		collectPackedEnumsInTypeDepth(tt.Value, out, depth+1)
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
				collectPackedEnumsInTypeDepth(payload, out, depth+1)
			}
		}
		for _, child := range tt.Children {
			collectPackedEnumsInTypeDepth(child, out, depth+1)
		}
	case *PackedVariantViewType:
		if tt != nil && tt.Enum != nil && tt.Enum.Packed {
			root := tt.Enum.Root()
			out[root.Name] = root
		}
	case *DArrayType:
		if tt != nil {
			collectPackedEnumsInTypeDepth(tt.Elem, out, depth+1)
		}
	case *StructType:
		if tt != nil {
			for _, field := range tt.Fields {
				collectPackedEnumsInTypeDepth(field.Type, out, depth+1)
			}
		}
	case *TupleType:
		if tt != nil {
			for _, field := range tt.Fields {
				collectPackedEnumsInTypeDepth(field.Type, out, depth+1)
			}
		}
	}
}
