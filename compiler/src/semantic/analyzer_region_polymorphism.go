package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
)

// RegionPolymorphicImplicitParamName is the synthetic name of the hidden region parameter threaded
// into a region-polymorphic function (docs/75). It carries the caller's region as an Arena& value;
// `new[auto]` in the body allocates into it, so the result lives in the caller's region. Exported so
// the backend recognizes the param when binding it at function entry.
const RegionPolymorphicImplicitParamName = "__region_auto"

// regionPolymorphicImplicitParamName is the package-internal alias kept for brevity at call sites.
const regionPolymorphicImplicitParamName = RegionPolymorphicImplicitParamName

// classifyRegionPolymorphicFunctions is the docs/75 pre-pass. It runs after every top-level FuncType
// exists but BEFORE any body is analyzed, so that when bodies (and their call sites — recursive ones
// included) are analyzed, every region-polymorphic callee already advertises its hidden region
// parameter. A function is region-polymorphic when a value-returning path hands back a value built
// with `new[auto]` (directly, or transitively via a call to an already-region-polymorphic function).
// The "transitive" case needs a fixpoint: a wrapper that `return helper()` only becomes
// region-polymorphic once `helper` is known to be.
func (a *Analyzer) classifyRegionPolymorphicFunctions(decls []scopedDecl) {
	funcs := collectRegionPolyCandidateFuncs(decls)
	if len(funcs) == 0 {
		return
	}
	changed := true
	for changed {
		changed = false
		for _, fn := range funcs {
			fnType := a.funcTypeForRegionPoly(fn)
			if fnType == nil || fnType.RegionPolymorphic {
				continue
			}
			// A function that threads an allocator (`Arena&` param) manages its own
			// allocation — same gate as functionBuildsAndReturnsLocalContainer and the
			// parser's maybeWrapFunctionBodyInAutoRegion: its returned node was built
			// into the explicit arena (`in alloc:`), not an adoptable inferred region,
			// so classifying it region-polymorphic would inject a spurious
			// `__region_auto` param and break its callers.
			a.regionPolyFn = fn
			if (a.functionReturnsRegionAllocatedValue(fn) && !funcHasArenaParam(fn)) || functionBuildsAndReturnsLocalContainer(fn) {
				fnType.RegionPolymorphic = true
				changed = true
			}
		}
	}
	a.regionPolyFn = nil
	// A region-polymorphic function whose body allocates containers outside any region
	// scope (e.g. a comprehension assigned to a local that is later returned) needs a
	// synthesized auto region: the backend adopts it into the threaded caller region
	// (regionPolyAutoAdopts), so the allocations land in the caller's arena. The parser
	// wraps the common shapes (return-position literals); this covers the rest.
	for _, fn := range funcs {
		fnType := a.funcTypeForRegionPoly(fn)
		if fnType == nil || !fnType.RegionPolymorphic || len(fn.Body) == 0 {
			continue
		}
		if bodyAllocatesInferredContainers(fn.Body) {
			wrapFuncBodyInLazyAutoRegion(fn)
		}
	}
	// A function returning a `static T&` asserts a PROGRAM-lifetime result. When such a
	// function builds local containers (the C-string-builder shape: `out: dstr = []`,
	// push bytes, `return (&out[0]).cast[static u8&]`), the only region that honors the
	// asserted lifetime is the permanent one — so bind its body's ambient region to perm.
	// This is what lets these builders be written with no region annotations at all.
	for _, fn := range funcs {
		fnType := a.funcTypeForRegionPoly(fn)
		if fnType == nil || fnType.RegionPolymorphic || funcHasArenaParam(fn) || len(fn.Body) == 0 {
			continue
		}
		if !returnTypeIsStaticRef(fn.ReturnType) {
			continue
		}
		// The parser may already have wrapped the body in a synthesized auto region
		// (which would scope the allocations to the FUNCTION — freed on return, the
		// opposite of the asserted static lifetime). Look through it and replace it.
		body := fn.Body
		if len(body) == 1 {
			if rs, isRegion := body[0].(*ast.RegionStmt); isRegion && rs != nil && isSynthesizedAutoRegion(rs.Name) {
				body = rs.Body
			}
		}
		if !bodyAllocatesInferredContainers(body) {
			continue
		}
		pos := body[0].Pos()
		fn.Body = []ast.Stmt{&ast.InStoreStmt{
			Position: pos,
			Store:    &ast.Ident{Position: pos, Name: "perm"},
			Body:     body,
		}}
	}
	// Inference-by-default for CALLERS: a non-region-polymorphic function that calls a
	// region-polymorphic one needs an ambient region to thread in. The parser's
	// maybeWrapFunctionBodyInAutoRegion can't see this case (callee polymorphism is a
	// cross-file semantic fact), so wrap such bodies here, after the fixpoint settled.
	a.wrapRegionPolyCallerBodies(funcs)
	// docs/74: only enums actually built with `new[auto]` somewhere in the program are region-backed
	// and eligible for implicit store threading; explicit-store enums must be left untouched.
	regionBackedPacked := a.collectRegionBackedPackedEnums(funcs)
	// docs/76: compute each function's TRANSITIVE region-backed store needs (signature ∪ what its
	// callees need) so mutually-recursive enum groups thread every required store across the call
	// graph, not just the one in each function's own signature.
	storeNeeds := a.computeTransitiveStoreNeeds(funcs, regionBackedPacked)
	// An export target must have NO implicit parameters (exports.go), so it can never receive a
	// threaded store — it owns a region and creates stores on demand. The generated test runner turns
	// each @test body into an export target (e.g. ctx_test_case_impl_0), so detect them structurally.
	exportTargets := map[string]bool{}
	for _, scoped := range decls {
		if ed, ok := scoped.Decl.(*ast.ExportFuncDecl); ok && ed != nil && ed.TargetName != "" {
			exportTargets[ed.TargetName] = true
		}
	}
	for _, fn := range funcs {
		fnType := a.funcTypeForRegionPoly(fn)
		if fnType == nil {
			continue
		}
		// Entry points (@test/@bench/main) and export targets own a region and create stores on demand
		// — they must NOT receive threaded store params (the runtime/export ABI calls them with none).
		// Every other function receives its full transitive store set threaded in, so a bare handle can
		// be built/matched without an explicit Store, even across a mutually-recursive enum group.
		// "Creates stores on demand" needs a region to host them: a store-creating function
		// (export target, entry point, or region owner) whose body makes store-needing calls
		// OUTSIDE any region scope gets a synthesized lazy auto region around its body
		// (previously this shape failed at LLVM lowering with "no active inferred region
		// arena", so only rejected code is affected).
		a.regionPolyFn = fn
		needsStoreHost := a.bodyCallsStoreNeedingOutsideRegion(fn.Body, storeNeeds, regionBackedPacked)
		a.regionPolyFn = nil
		if fn.Name != "" && exportTargets[fn.Name] {
			if needsStoreHost {
				wrapFuncBodyInLazyAutoRegion(fn)
			}
			continue
		}
		if fnType.RegionPolymorphic {
			a.injectRegionPolymorphicParam(fnType)
		}
		// A function that owns a region (entry/export, or any `in auto:` block) creates region-backed
		// stores on demand — possibly one per region in a loop — so it gets only its SIGNATURE stores
		// threaded (the data flowing across its boundary), never the transitive callee set. Only
		// region-LESS functions (pure consumers/builders like sumT/sumF/leaf) receive the full
		// transitive set, because they have no region in which to create a callee's store themselves.
		if funcIsRegionStoreEntryPoint(fn) || funcOwnsRegion(fn) {
			a.injectInferredPackedStoreParams(fnType, regionBackedPacked)
			if needsStoreHost {
				wrapFuncBodyInLazyAutoRegion(fn)
			}
		} else {
			a.injectStoreNeeds(fnType, storeNeeds[fnType])
		}
	}
}

// bodyCallsStoreNeedingOutsideRegion reports whether the statements contain a direct call to a
// function with non-empty transitive packed-store needs that is not already inside a region scope.
// Such a call site synthesizes an on-demand store, which requires an active region to host it.
// Mirrors bodyCallsRegionPolyOutsideRegion's skip rules (region/in-store subtrees, lambdas).
// typeExprNeedsRegionBackedStore reports whether a syntactic type expression names a type that
// carries region-backed packed enum handles (directly or through containers/wrappers).
func (a *Analyzer) typeExprNeedsRegionBackedStore(typeExpr ast.TypeExpr, regionBacked map[string]bool) bool {
	named, ok := typeExpr.(*ast.NamedType)
	if !ok || named == nil {
		return false
	}
	t, ok := a.namedTypes[named.Name]
	if !ok || t == nil {
		return false
	}
	enums := map[string]*EnumType{}
	collectPackedEnumsInType(t, enums)
	for name, et := range enums {
		if regionBacked[name] && et != nil && !et.HandleIsPointer() {
			return true
		}
	}
	return false
}

func (a *Analyzer) bodyCallsStoreNeedingOutsideRegion(stmts []ast.Stmt, storeNeeds map[*FuncType]map[string]*EnumType, regionBacked map[string]bool) bool {
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
			switch n := v.Interface().(type) {
			case *ast.RegionStmt:
				if len(n.Body) != 0 {
					return // scoped region hosts on-demand stores for its body
				}
			case *ast.InStoreStmt:
				return // `in <owner>:` establishes an ambient owner
			case *ast.LambdaExpr:
				return
			case *ast.CallExpr:
				if ft := a.regionPolyCalleeFuncType(n); ft != nil && len(storeNeeds[ft]) != 0 {
					found = true
					return
				}
				if fieldExpr, ok := unwrapParenForRegionPoly(n.Func).(*ast.FieldExpr); ok {
					for _, ft := range a.regionPolyProtocolMethodImplFuncTypes(fieldExpr) {
						if len(storeNeeds[ft]) != 0 {
							found = true
							return
						}
					}
				}
			}
			// A cast can resolve to a user `__cast__` hook — a call in disguise. This pre-pass
			// runs before bodies are analyzed (resolvedCastHooks is still empty), so detect the
			// site syntactically: if the cast TARGET type carries region-backed packed handles,
			// the hook (if any) carries the matching store param and this site synthesizes the
			// on-demand store just like a CallExpr.
			if castExpr, isCast := v.Interface().(*ast.CastExpr); isCast {
				if a.typeExprNeedsRegionBackedStore(castExpr.Target, regionBacked) {
					found = true
					return
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
	rec(reflect.ValueOf(stmts))
	return found
}

// collectRegionPolyCandidateFuncs flattens the top-level and impl-member function declarations into
// a single list for the classification fixpoint.
func collectRegionPolyCandidateFuncs(decls []scopedDecl) []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, scoped := range decls {
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			out = append(out, n)
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					out = append(out, fn)
				}
			}
		}
	}
	return out
}

func (a *Analyzer) funcTypeForRegionPoly(fn *ast.FuncDecl) *FuncType {
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return nil
	}
	fnType, _ := sym.Type.(*FuncType)
	return fnType
}

// injectRegionPolymorphicParam appends the hidden region parameter (an Arena&, named
// `__region_auto`) to a region-polymorphic function's type, mirroring the tree-store implicit-param
// injection. Idempotent: a second call is a no-op.
func (a *Analyzer) injectRegionPolymorphicParam(fnType *FuncType) {
	if fnType == nil || funcTypeHasImplicitParam(fnType, regionPolymorphicImplicitParamName) {
		return
	}
	arenaType := a.namedTypes["Arena"]
	if arenaType == nil {
		return
	}
	fnType.Params = append(fnType.Params, &RefType{Elem: arenaType, State: RefStateNonNull, Storage: RefStorageAny, Mutable: true})
	fnType.ImplicitParamNames = append(fnType.ImplicitParamNames, regionPolymorphicImplicitParamName)
}

// functionReturnsRegionAllocatedValue reports whether any value-returning path of fn hands back a
// value whose result is a `new[auto]` allocation, or a call to an already-region-polymorphic
// function. Walks only statement bodies (not lambda expression bodies), so a `return` inside a
// nested closure is correctly excluded.
func (a *Analyzer) functionReturnsRegionAllocatedValue(fn *ast.FuncDecl) bool {
	found := false
	// Locals fed by region-allocated values (a `result` accumulated across branches from
	// region-polymorphic calls / bare region-backed constructors): returning one is returning a
	// region-allocated value — the same local-tracking functionBuildsAndReturnsLocalContainer
	// does for container literals.
	regionLocals := map[string]bool{}
	// regiony extends exprResultIsRegionAllocated with local knowledge: an ident naming a
	// region-fed local, a struct literal wrapping one, or a region-less container literal
	// (allocated in the inferred region) all carry region-allocated data.
	var regiony func(value ast.Expr) bool
	regiony = func(value ast.Expr) bool {
		value = unwrapParenForRegionPoly(value)
		switch e := value.(type) {
		case *ast.Ident:
			return regionLocals[e.Name]
		case *ast.StructLitExpr:
			for _, arg := range e.Args {
				if arg != nil && regiony(arg) {
					return true
				}
			}
		case *ast.ListLitExpr:
			// A region-less container literal allocates into the inferred region — even an
			// empty `[]` seed that gets pushed into (the build-local-return shape).
			if e.Owner == nil {
				return true
			}
			for _, item := range e.Elems {
				if item != nil && regiony(item) {
					return true
				}
			}
		case *ast.ListComprehensionExpr:
			// A region-less comprehension (`[x for x in xs]`, list/dict/set) allocates its
			// result into the inferred region exactly like a container literal — returning one
			// makes the function region-polymorphic. The parser wraps such a body in a
			// synthesized auto region (functionBodyNeedsAutoRegion's ReturnStmt case) so the
			// comprehension has an active scope; classification here adopts that region into
			// the caller so the result outlives the call.
			if e.Owner == nil {
				return true
			}
		case *ast.QueryExpr:
			// An `each` query (`X{...} for each v in src`) builds a fresh darray in the
			// inferred region — the QueryExpr analogue of the list comprehension above. It
			// MUST classify region-poly: if the function also calls a region-poly callee,
			// wrapRegionPolyCallerBodies gives its body a synthesized LOCAL region, so the
			// query compiles — but without this classification the local region is never
			// adopted by the caller and dies at return, leaving the returned darray's buffer
			// dangling (count reads fine, element reads segfault).
			if e.Kind == ast.QueryExprEach && e.Owner == nil {
				return true
			}
		case *ast.TupleExpr:
			// Lowered grammar try-productions return (matched, committed, value) tuples;
			// a region-allocated value inside makes the whole return region-allocated.
			for _, item := range e.Elems {
				if item != nil && regiony(item) {
					return true
				}
			}
		}
		return a.exprResultIsRegionAllocated(value)
	}
	var collect func(stmts []ast.Stmt)
	collect = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if s.Value != nil && regiony(s.Value) {
					regionLocals[s.Name] = true
				}
			case *ast.AssignStmt:
				if target, ok := s.Target.(*ast.Ident); ok && target != nil && s.Value != nil && regiony(s.Value) {
					regionLocals[target.Name] = true
				}
			case *ast.IfStmt:
				collect(s.Then)
				for _, elif := range s.Elifs {
					collect(elif.Body)
				}
				collect(s.Else)
			case *ast.WhileStmt:
				collect(s.Body)
			case *ast.ForStmt:
				collect(s.Body)
			case *ast.IterForStmt:
				collect(s.Body)
			case *ast.ScopeStmt:
				collect(s.Body)
			case *ast.CanStmt:
				collect(s.Body)
			case *ast.InStoreStmt:
				collect(s.Body)
			case *ast.RegionStmt:
				// The parser wraps allocating bodies in a synthesized `__auto_*` region; the
				// values inside are exactly the ones a region-polymorphic caller adopts, so
				// the classifier must see through it. Explicit named regions stay opaque
				// (their values die with the region — returning them is a separate error).
				if isSynthesizedAutoRegion(s.Name) {
					collect(s.Body)
				}
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					collect(arm.Body)
				}
			}
		}
	}
	if fn != nil {
		// Fixpoint: locals feed locals (`d := ctor; decls := [d]; return Result{decls}`).
		for {
			before := len(regionLocals)
			collect(fn.Body)
			if len(regionLocals) == before {
				break
			}
		}
	}
	var walk func(stmts []ast.Stmt)
	check := func(value ast.Expr) {
		if value != nil && regiony(value) {
			found = true
		}
	}
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if found {
				return
			}
			switch s := stmt.(type) {
			case *ast.ReturnStmt:
				check(s.Value)
			case *ast.IfStmt:
				walk(s.Then)
				for _, elif := range s.Elifs {
					walk(elif.Body)
				}
				walk(s.Else)
			case *ast.WhileStmt:
				walk(s.Body)
			case *ast.ForStmt:
				walk(s.Body)
			case *ast.IterForStmt:
				walk(s.Body)
			case *ast.ParallelForStmt:
				walk(s.Body)
			case *ast.ScopeStmt:
				walk(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					walk(arm.Body)
				}
			case *ast.CanStmt:
				walk(s.Body)
			case *ast.RegionStmt:
				walk(s.Body)
			case *ast.InStoreStmt:
				walk(s.Body)
			}
		}
	}
	walk(fn.Body)
	return found
}

// functionBuildsAndReturnsLocalContainer detects the build-local-return shape (Stage 1 of the
// region-return-inference plan): a function whose SIGNATURE return type is an owned container
// (`darray`/`dict`/`set`/`dstr`) and which RETURNS a local that was declared as a container literal
// (`out: darray[T] = []`, `out: dict[K,V] = {}`, `out: set[T] = {}`) built up in the body.
// Such a function is region-polymorphic: its
// `out = []` opens a synthesized `__auto_*` region (maybeWrapFunctionBodyInAutoRegion wraps the
// whole body, the `return out` included), and once classified region-polymorphic the backend's
// regionPolyAutoAdopts threads+adopts that region into the caller's `__region_auto`, so the result
// outlives the call. This is the syntactic, pre-pass-safe analogue of the `new[auto]` detection:
// it needs no type info (return-type annotation + container-literal initializer are both syntactic),
// so classification precedes all body analysis and every caller threads the region in.
//
// It deliberately matches ONLY a returned local Ident declared as a container literal — not a
// returned param, field, or call result — so it never over-classifies a forwarding function. A
// false match is in any case safe (an unused threaded arena param), but the conservative shape keeps
// classification honest. The escape suppression (region_containers.go) is the actual safety gate and
// only fires for a synthesized `__auto_*` region, mirroring regionPolyAutoAdopts exactly.
func functionBuildsAndReturnsLocalContainer(fn *ast.FuncDecl) bool {
	if fn == nil || !returnTypeIsOwnedContainer(fn.ReturnType) {
		return false
	}
	// A function that threads an allocator (`Arena&` param) manages its own allocation: the parser
	// does NOT wrap its body in a synthesized auto region (maybeWrapFunctionBodyInAutoRegion's
	// hasArenaParam guard), so its returned container is NOT in an adoptable `__auto_*` region.
	// Classifying it region-polymorphic would inject a spurious `__region_auto` param and break its
	// callers (which build into the explicit arena, not an inferred region). Exclude it — mirror the
	// parser gate exactly.
	if funcHasArenaParam(fn) {
		return false
	}
	// Collect locals declared as a region-LESS container literal that live in the inferred region:
	// the bare body or a synthesized `__auto_*` region scope. Deliberately do NOT descend into an
	// explicit `region NAME:` (a non-auto RegionStmt) or `in <arena>:` (InStoreStmt) — a container
	// built there belongs to that explicit region, is not adopted, and returning it still (correctly)
	// escapes. Only the synthesized-auto-region container is threaded+adopted from the caller.
	literalLocals := map[string]bool{}
	var collect func(stmts []ast.Stmt)
	collect = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if _, ok := unwrapParenForRegionPoly(s.Value).(*ast.ListLitExpr); ok && typeExprIsRegionless(s.Type) {
					literalLocals[s.Name] = true
				}
			case *ast.IfStmt:
				collect(s.Then)
				for _, elif := range s.Elifs {
					collect(elif.Body)
				}
				collect(s.Else)
			case *ast.WhileStmt:
				collect(s.Body)
			case *ast.ForStmt:
				collect(s.Body)
			case *ast.IterForStmt:
				collect(s.Body)
			case *ast.ScopeStmt:
				collect(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					collect(arm.Body)
				}
			case *ast.CanStmt:
				collect(s.Body)
			case *ast.RegionStmt:
				// Only a synthesized auto region threads+adopts; an explicit `region NAME:` does not.
				if isSynthesizedAutoRegion(s.Name) {
					collect(s.Body)
				}
			}
		}
	}
	collect(fn.Body)
	if len(literalLocals) == 0 {
		return false
	}
	found := false
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if found {
				return
			}
			switch s := stmt.(type) {
			case *ast.ReturnStmt:
				if id, ok := unwrapParenForRegionPoly(s.Value).(*ast.Ident); ok && literalLocals[id.Name] {
					found = true
				}
			case *ast.IfStmt:
				walk(s.Then)
				for _, elif := range s.Elifs {
					walk(elif.Body)
				}
				walk(s.Else)
			case *ast.WhileStmt:
				walk(s.Body)
			case *ast.ForStmt:
				walk(s.Body)
			case *ast.IterForStmt:
				walk(s.Body)
			case *ast.ScopeStmt:
				walk(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					walk(arm.Body)
				}
			case *ast.CanStmt:
				walk(s.Body)
			case *ast.RegionStmt:
				walk(s.Body)
			case *ast.InStoreStmt:
				walk(s.Body)
			}
		}
	}
	walk(fn.Body)
	return found
}

// funcHasArenaParam reports whether any explicit parameter is an `Arena` (modulo `mutable`/`&`
// wrappers) — such a function threads its own allocator and is excluded from build-local-return
// classification, mirroring the parser's maybeWrapFunctionBodyInAutoRegion gate.
func funcHasArenaParam(fn *ast.FuncDecl) bool {
	for i := range fn.Params {
		if typeExprNamedIs(fn.Params[i].Type, "Arena") {
			return true
		}
	}
	return false
}

// typeExprNamedIs reports whether a type expression names `name` after stripping `mutable`/`&`.
func typeExprNamedIs(typ ast.TypeExpr, name string) bool {
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.RefType:
			typ = t.Elem
		default:
			nt, ok := typ.(*ast.NamedType)
			return ok && nt.Name == name
		}
	}
}

// typeExprIsRegionless reports whether a container local's declared type carries NO explicit `@r`
// region annotation — only a region-less container literal lands in the inferred (adoptable) region.
// A nil (untyped) declaration is conservatively NOT treated as region-less build-local-return: the
// typed `out: darray[T] = []` form is the Stage 1 canonical shape (and matches join/make signatures).
func typeExprIsRegionless(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	switch t := typ.(type) {
	case *ast.BuiltinTypeExpr:
		return t.Region == ""
	case *ast.NamedType:
		return t.Name == "dstr" // dstr has no region-annotation surface; always region-less here
	}
	return false
}

// returnTypeIsOwnedContainer reports whether a function's return-type annotation is a growable owned
// container — `darray`/`dict`/`set` (BuiltinTypeExpr) or `dstr` (NamedType, the u8 darray). Views
// (`sview`/`cstr`) and `array` (fixed) are excluded: they are not region-owned growables.
func returnTypeIsOwnedContainer(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	switch t := typ.(type) {
	case *ast.NamedType:
		return t.Name == "dstr"
	case *ast.BuiltinTypeExpr:
		switch t.Name {
		case "darray", "dict", "set":
			return t.Region == ""
		}
	}
	return false
}

// exprResultIsRegionAllocated reports whether the RESULT of an expression is a fresh region
// allocation (a `new[auto]` modulo parentheses) or the result of an already-region-polymorphic call.
// It deliberately does NOT descend into arguments: `f(new[auto] X())` yields f's result, not the
// new[auto], so it is not region-allocated unless f itself is region-polymorphic.
func (a *Analyzer) exprResultIsRegionAllocated(value ast.Expr) bool {
	value = unwrapParenForRegionPoly(value)
	switch e := value.(type) {
	case *ast.AllocExpr:
		if e == nil {
			return false
		}
		if e.AutoRegion {
			return true
		}
		// Bare `new` defaults to new[auto] (region-allocated) unless it is a
		// packed-enum constructor, where only region-backed (recursive-plain)
		// enums take the inferred-region path — mirroring the bare-constructor
		// rule (regionBackedEnumConstructor) and analyzeAllocExprWithExpected.
		if e.Owner == nil {
			if enumType, _, ok := a.packedAllocConstructorInfo(e.Value); ok && enumType != nil && enumType.Packed {
				return enumType.RecursivePlain
			}
			return true
		}
		return false
	case *ast.CallExpr:
		if ft := a.regionPolyCalleeFuncType(e); ft != nil && ft.RegionPolymorphic {
			return true
		}
		// A bare constructor of a region-backed (recursive-plain) enum is an implicit new[auto]
		// allocation (docs/76 Slice 0b), so a function returning one is region-polymorphic.
		if et, ok := a.regionBackedEnumConstructor(e); ok && et != nil {
			return true
		}
	case *ast.StructLitExpr:
		// Pre-analysis, `Name(args)` parses as a StructLitExpr whether Name is a struct
		// or a function — so a constructor-style CALL of a region-polymorphic function
		// (`return Manager(Users_New(), -1)` / `return Users_New()`) lands here, not in
		// the CallExpr case. Resolve the name: a region-poly function makes the value
		// region-allocated.
		if sym, _, ok := a.lookupVisibleGlobal(e.Name); ok && sym != nil {
			if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil && ft.RegionPolymorphic {
				return true
			}
		}
		// A struct literal wrapping region-allocated values (the parse-result pattern:
		// `return Result{ast: ..., decls: decls}`) carries its fields' region — returning
		// it returns region-allocated data, so the function must thread the caller region
		// instead of freeing a local auto-region under the returned handles.
		for _, arg := range e.Args {
			if arg == nil {
				continue
			}
			if a.exprResultIsRegionAllocated(arg) {
				return true
			}
			// Region-less container literals/comprehensions as constructor args allocate
			// into the inferred region (the regiony() rule, applied transitively here).
			if lit, isLit := unwrapParenForRegionPoly(arg).(*ast.ListLitExpr); isLit && lit != nil && lit.Owner == nil {
				return true
			}
			if comp, isComp := unwrapParenForRegionPoly(arg).(*ast.ListComprehensionExpr); isComp && comp != nil && comp.Owner == nil {
				return true
			}
		}
	}
	return false
}

// regionBackedEnumConstructor reports the enum if a call is a bare constructor of a region-backed
// (recursive-plain) enum — `Expr.Add(...)` where Expr is a promoted plain enum. Explicit `packed
// enum`s (e.g. an explicit-store JSON DOM) are NOT region-backed-by-default and are excluded, so
// their bare constructors keep the explicit-store path.
func (a *Analyzer) regionBackedEnumConstructor(call *ast.CallExpr) (*EnumType, bool) {
	if call == nil {
		return nil, false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed || !enumType.RecursivePlain {
		return nil, false
	}
	return enumType, true
}

func unwrapParenForRegionPoly(value ast.Expr) ast.Expr {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok || paren == nil {
			return value
		}
		value = paren.Inner
	}
}

// defineRegionPolymorphicParamSymbol binds the hidden `__region_auto` Arena& parameter as a local
// symbol in a region-polymorphic function's body scope, so the threaded recursive-call argument
// (`Ident{__region_auto}`) resolves and implicitBindingsForCurrentFunction surfaces it.
func (a *Analyzer) defineRegionPolymorphicParamSymbol(fn *ast.FuncDecl, fnType *FuncType) {
	if fnType == nil || a.namedTypes["Arena"] == nil {
		return
	}
	idx := -1
	for i, name := range fnType.ImplicitParamNames {
		if name == regionPolymorphicImplicitParamName {
			idx = funcTypeExplicitParamCount(fnType) + i
			break
		}
	}
	if idx < 0 {
		return
	}
	arenaRef := &RefType{Elem: a.namedTypes["Arena"], State: RefStateNonNull, Storage: RefStorageAny, Mutable: true}
	sym := &Symbol{Name: regionPolymorphicImplicitParamName, Kind: SymbolParam, Type: arenaRef, Node: fn, ParamIndex: idx}
	a.defineLocal(sym, fn.Pos())
}

// defineRegionParamValueSymbols makes each explicit `[@owner]` parameter referenceable as an
// Arena VALUE inside the function body (not just as an `@owner` annotation). A region param is bound
// as a SymbolRegion exactly like a `region owner(...):` declaration, so `owner` resolves where an
// arena value is expected — e.g. a runtime container constructor that threads an explicit arena
// (`indexmap.new(owner)`, SoA stores). The backend already keys its region environment by name
// (state.regions), so it resolves the reference to the threaded hidden Arena& param. This is the
// no-Arena-field replacement for `owner: mutable Arena&` state fields: the region rides the type,
// and the value form is recovered from the same region param.
func (a *Analyzer) defineRegionParamValueSymbols(fn *ast.FuncDecl) {
	if fn == nil || len(fn.RegionParams) == 0 {
		return
	}
	arenaType, ok := a.namedTypes["Arena"]
	if !ok || arenaType == nil {
		return
	}
	arenaRef := &RefType{Elem: arenaType, State: RefStateNonNull, Storage: RefStorageAny, Mutable: true}
	for _, name := range fn.RegionParams {
		if _, exists := a.currentScope.Lookup(name); exists {
			continue // an explicit value parameter of the same name wins
		}
		sym := &Symbol{Name: name, Kind: SymbolRegion, Type: arenaRef, Node: fn, Mutable: true}
		a.defineLocal(sym, fn.Pos())
		if a.currentRegions != nil {
			a.currentRegions[sym] = regionState{Backing: normalizeBacking(""), DeclScope: a.currentScope}
		}
	}
}

// regionPolymorphicCallerRegionArg produces the expression that threads the caller's ambient inferred
// region into a region-polymorphic call. When the caller is itself region-polymorphic, its hidden
// `__region_auto` parameter carries the region through. Otherwise the active `in auto:` region (the
// same one `new[auto]` allocates into) supplies it. Returns false when no region is active.
func (a *Analyzer) regionPolymorphicCallerRegionArg() (ast.Expr, bool) {
	if a.currentFuncType != nil && a.currentFuncType.RegionPolymorphic {
		return &ast.Ident{Name: regionPolymorphicImplicitParamName}, true
	}
	if region := a.activeContainerRegionName(); region != "" {
		return &ast.Ident{Name: region}, true
	}
	return nil, false
}

// wrapRegionPolyCallerBodies wraps the body of each function that calls a region-polymorphic
// callee outside any region scope in a synthesized lazy auto region, so the call site has a
// region to thread (mirroring the parser's maybeWrapFunctionBodyInAutoRegion, which handles
// direct allocations but cannot know callee polymorphism). Only non-region-polymorphic
// functions without an Arena param are wrapped: a region-polymorphic caller threads its own
// `__region_auto`, and an Arena-threading function manages allocation itself.
func (a *Analyzer) wrapRegionPolyCallerBodies(funcs []*ast.FuncDecl) {
	defer func() { a.regionPolyFn = nil }()
	for _, fn := range funcs {
		a.regionPolyFn = fn
		fnType := a.funcTypeForRegionPoly(fn)
		if fnType == nil || funcHasArenaParam(fn) || len(fn.Body) == 0 {
			continue
		}
		if fnType.RegionPolymorphic {
			continue // threads its caller's region; no scope of its own needed
		}
		if !a.bodyCallsRegionPolyOutsideRegion(fn.Body) {
			continue
		}
		// Tighten loops first (the semantic analogue of the parser's wrapReclaimableLoopBodies):
		// a loop whose region-poly call results are iteration-local gets a per-iteration region,
		// so each iteration's trees are reclaimed instead of accumulating for the whole loop.
		a.tightenRegionPolyLoopBodies(fn.Body)
		if !a.bodyCallsRegionPolyOutsideRegion(fn.Body) {
			continue // every region-poly call now sits inside a loop region
		}
		wrapFuncBodyInLazyAutoRegion(fn)
	}
}

// wrapFuncBodyInLazyAutoRegion wraps a function body in a compiler-synthesized lazy auto
// region (the semantic analogue of the parser's maybeWrapFunctionBodyInAutoRegion).
func wrapFuncBodyInLazyAutoRegion(fn *ast.FuncDecl) {
	if fn == nil || len(fn.Body) == 0 {
		return
	}
	pos := fn.Body[0].Pos()
	fn.Body = []ast.Stmt{&ast.RegionStmt{
		Position: pos,
		Name:     fmt.Sprintf("__auto_%d", pos.Offset),
		Lazy:     true,
		Body:     fn.Body,
	}}
}

// tightenRegionPolyLoopBodies wraps reclaimable loop bodies containing region-poly calls in a
// per-iteration lazy region. Deliberately does NOT descend into explicit region/in-store scopes:
// code there already compiled with accumulation semantics and must not change behavior. Only
// previously-rejected code (region-poly calls with no ambient region) is affected.
func (a *Analyzer) tightenRegionPolyLoopBodies(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			s.Body = a.tightenRegionPolyLoopBody(s.Body)
		case *ast.WhileStmt:
			s.Body = a.tightenRegionPolyLoopBody(s.Body)
		case *ast.IterForStmt:
			s.Body = a.tightenRegionPolyLoopBody(s.Body)
		case *ast.IfStmt:
			a.tightenRegionPolyLoopBodies(s.Then)
			a.tightenRegionPolyLoopBodies(s.Else)
		case *ast.CanStmt:
			a.tightenRegionPolyLoopBodies(s.Body)
		case *ast.ScopeStmt:
			a.tightenRegionPolyLoopBodies(s.Body)
		case *ast.MatchStmt:
			for i := range s.Arms {
				a.tightenRegionPolyLoopBodies(s.Arms[i].Body)
			}
		}
	}
}

func (a *Analyzer) tightenRegionPolyLoopBody(body []ast.Stmt) []ast.Stmt {
	a.tightenRegionPolyLoopBodies(body) // tighten nested loops first
	if !a.loopBodyRegionPolyReclaimable(body) {
		return body
	}
	pos := body[0].Pos()
	return []ast.Stmt{&ast.RegionStmt{
		Position: pos,
		Name:     fmt.Sprintf("__auto_%d", pos.Offset),
		Lazy:     true,
		Body:     body,
	}}
}

// loopBodyRegionPolyReclaimable reports whether a loop body's region-poly allocations are all
// iteration-local, so a per-iteration region is safe and useful. Conservative: a region-allocated
// RESULT bound to a name must be iteration-local; one assigned to an existing (possibly outer)
// target disqualifies the loop; growing an outer container disqualifies it. A region-poly call
// consumed within an expression (`acc <- acc + check(make(d))` yields a scalar) is always fine.
func (a *Analyzer) loopBodyRegionPolyReclaimable(body []ast.Stmt) bool {
	if len(body) == 0 || !a.bodyCallsRegionPolyOutsideRegion(body) {
		return false
	}
	if ast.LoopBodyGrowsOuterContainer(body) {
		return false
	}
	for _, stmt := range body {
		switch s := stmt.(type) {
		case *ast.VarDeclStmt:
			if a.exprResultIsRegionAllocated(s.Value) && !ast.AllocationIsIterationLocal(s.Name, body) {
				return false
			}
		case *ast.AssignStmt:
			if a.exprResultIsRegionAllocated(s.Value) {
				return false // the target may outlive the iteration
			}
		}
	}
	return true
}

// bodyCallsRegionPolyOutsideRegion reports whether the statements contain a direct call to a
// region-polymorphic function that is not already inside a region scope (`region NAME:` /
// `in owner:` subtrees supply their own ambient region and are skipped). Lambda bodies are
// skipped too: a lambda runs in its eventual caller's context, not this function's.
func (a *Analyzer) bodyCallsRegionPolyOutsideRegion(stmts []ast.Stmt) bool {
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
			switch n := v.Interface().(type) {
			case *ast.RegionStmt:
				if len(n.Body) != 0 {
					return // scoped region supplies the ambient region for its body
				}
			case *ast.InStoreStmt:
				return // `in <owner>:` establishes an ambient owner
			case *ast.LambdaExpr:
				return
			case *ast.CallExpr:
				if ft := a.regionPolyCalleeFuncType(n); ft != nil && ft.RegionPolymorphic {
					found = true
					return
				}
			case *ast.StructLitExpr:
				// Pre-analysis, `Name(args)` parses as a StructLitExpr whether Name is a
				// struct or a function — a constructor-style call of a region-polymorphic
				// function needs an ambient region exactly like a CallExpr.
				if n != nil && n.Name != "" {
					if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok && sym != nil {
						if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil && ft.RegionPolymorphic {
							found = true
							return
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
	rec(reflect.ValueOf(stmts))
	return found
}

// regionPolyCalleeFuncType resolves a direct call's callee to its FuncType when the callee is a plain
// identifier naming a visible global. Indirect or method calls return nil (the transitive trigger
// simply does not fire — the direct new[auto] trigger still covers them).
func (a *Analyzer) regionPolyCalleeFuncType(call *ast.CallExpr) *FuncType {
	if call == nil {
		return nil
	}
	name := ""
	callee := unwrapParenForRegionPoly(call.Func)
	// Unwrap generic value-form specialization: `fn[A, B](...)` parses as a SpecializeExpr
	// callee, and single-arg `fn[T](...)` as an IndexExpr over the function identifier (the
	// analyzer reinterprets it later via AsSpecialize). For ordinary indexing the Object names
	// a variable, whose symbol type is not a FuncType, so the lookup below returns nil anyway.
	switch wrapped := callee.(type) {
	case *ast.SpecializeExpr:
		callee = unwrapParenForRegionPoly(wrapped.Operand)
	case *ast.IndexExpr:
		callee = unwrapParenForRegionPoly(wrapped.Object)
	}
	switch callee := callee.(type) {
	case *ast.Ident:
		name = callee.Name
	case *ast.FieldExpr:
		// `B.method(...)` where B is a protocol-bounded generic param of the function
		// under classification: the concrete impl is specialization-dependent, so the
		// call is region-polymorphic if ANY impl of the protocol method is.
		if ft := a.regionPolyProtocolMethodFuncType(callee); ft != nil {
			return ft
		}
		// UFCS method call `self.helper(...)`: methods are plain globals, so
		// resolve by the field name when it names a visible function.
		name = callee.Field
	default:
		return nil
	}
	if name == "" {
		return nil
	}
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || sym == nil {
		return nil
	}
	fnType, _ := sym.Type.(*FuncType)
	return fnType
}

// regionPolyProtocolMethodFuncType resolves `B.method(...)` — B a generic param of the
// function under classification (a.regionPolyFn) bounded by a protocol — to a
// region-polymorphic impl method FuncType, if any impl of that protocol provides one.
// The dispatch target is specialization-dependent, so classification must be the union
// over impls: if any impl threads a caller region, the generic caller must too (a
// specialization with a value impl merely carries an unused Arena&).
func (a *Analyzer) regionPolyProtocolMethodFuncType(callee *ast.FieldExpr) *FuncType {
	for _, ft := range a.regionPolyProtocolMethodImplFuncTypes(callee) {
		if ft.RegionPolymorphic {
			return ft
		}
	}
	return nil
}

// regionPolyProtocolMethodImplFuncTypes returns the FuncTypes of every impl of the
// protocol method named by `B.method(...)`, where B is a protocol-bounded generic param
// of the function under classification (a.regionPolyFn). Empty outside the pre-pass or
// when the callee is not such a dispatch.
func (a *Analyzer) regionPolyProtocolMethodImplFuncTypes(callee *ast.FieldExpr) []*FuncType {
	fn := a.regionPolyFn
	if fn == nil || callee == nil || len(fn.GenericParams) == 0 {
		return nil
	}
	obj, ok := unwrapParenForRegionPoly(callee.Object).(*ast.Ident)
	if !ok || obj == nil {
		return nil
	}
	bound := ""
	for _, gp := range fn.GenericParams {
		if gp.Name == obj.Name {
			bound = gp.InterfaceBound
			break
		}
	}
	if bound == "" {
		return nil
	}
	_, ifaceName, ok := a.lookupVisibleStaticInterface(bound)
	if !ok {
		return nil
	}
	var out []*FuncType
	for _, impl := range a.staticImpls {
		if impl == nil || impl.InterfaceName != ifaceName {
			continue
		}
		sym := impl.Methods[callee.Field]
		if sym == nil {
			continue
		}
		if ft, ok := sym.Type.(*FuncType); ok && ft != nil {
			out = append(out, ft)
		}
	}
	return out
}

// returnTypeIsStaticRef reports whether a syntactic return type is a reference with
// `static` storage (peeling mutable/optional wrappers): `static u8&`, `mutable static T&?`.
func returnTypeIsStaticRef(typ ast.TypeExpr) bool {
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.OptionalTypeExpr:
			typ = t.Value
		case *ast.RefType:
			return t.Storage == ast.RefStorageStatic
		default:
			return false
		}
	}
}

// bodyAllocatesInferredContainers reports whether the statements contain a region-less
// container literal or comprehension outside any explicit region/in-store scope — the
// allocations a synthesized ambient region would capture.
func bodyAllocatesInferredContainers(stmts []ast.Stmt) bool {
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
			switch n := v.Interface().(type) {
			case *ast.RegionStmt:
				if len(n.Body) != 0 {
					return
				}
			case *ast.InStoreStmt:
				return
			case *ast.LambdaExpr:
				return
			case *ast.ListLitExpr:
				if n.Owner == nil {
					found = true
					return
				}
			case *ast.ListComprehensionExpr:
				if n.Owner == nil {
					found = true
					return
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
	rec(reflect.ValueOf(stmts))
	return found
}
