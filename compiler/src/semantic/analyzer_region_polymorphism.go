package semantic

import (
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
	// Each candidate's enclosing namespace + usings, so the region-allocated-return
	// detection below resolves UNQUALIFIED type names in a module body (e.g. `Val.Arr`
	// referring to `M.Val`) the way the body's own analysis later will. Without this a
	// module-scoped builder that returns a packed-enum value built from a local container
	// fails the `regionBackedEnumConstructor` check (the bare name `Val` doesn't resolve
	// to `M.Val`), so it never gets its `__region_auto` param and the container backing
	// dangles after return.
	fnScope := map[*ast.FuncDecl]scopedDecl{}
	for _, scoped := range decls {
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			fnScope[n] = scoped
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					fnScope[fn] = scoped
				}
			}
		}
	}
	// Index candidate functions by simple name so a constructor call shadowed by a
	// same-named struct (e.g. `Parser(args)` with both `struct Parser` and
	// `def Parser(...)->Parser`) still resolves to the constructor's FuncType. The
	// stored *FuncType is the same pointer mutated below, so RegionPolymorphic reads
	// stay live across the fixpoint.
	type regionPolyCandidate struct {
		fn           *ast.FuncDecl
		fnType       *FuncType
		scope        scopedDecl
		localFactory bool
	}
	candidates := make([]regionPolyCandidate, 0, len(funcs))
	a.regionPolyCandidateFnTypes = map[string][]*FuncType{}
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		if ft := a.funcTypeForRegionPoly(fn); ft != nil {
			if fn.Name != "" {
				key := simpleTypeNameSegment(fn.Name)
				a.regionPolyCandidateFnTypes[key] = append(a.regionPolyCandidateFnTypes[key], ft)
			}
			// Explicit arenas exclude every route to these implicit-region facts.
			if !funcHasArenaParam(fn) {
				candidates = append(candidates, regionPolyCandidate{
					fn: fn, fnType: ft, scope: fnScope[fn],
					localFactory: functionBuildsAndReturnsLocalContainer(fn) || functionBuildsAndReturnsLocalStruct(fn, ft),
				})
			}
		}
	}
	defer func() { a.regionPolyCandidateFnTypes = nil }()
	a.regionPolyReturnsCallerArena = map[*FuncType]bool{}
	changed := true
	for changed {
		changed = false
		for _, candidate := range candidates {
			fn, fnType, sc := candidate.fn, candidate.fnType, candidate.scope
			// The fixpoint only adds facts. A settled result needs no further
			// walk, and syntactic factory results do not depend on any callee.
			if a.regionPolyReturnsCallerArena[fnType] && fnType.RegionPolymorphic {
				continue
			}
			a.regionPolyFn = fn
			isPoly := false
			returnsCallerArena := candidate.localFactory
			a.withResolutionContext(sc.Namespace, sc.Usings, func() {
				returnsCallerArena = returnsCallerArena || a.functionReturnsRegionAllocatedValue(fn)
				// docs/125: a function that WRITES a region-backed packed handle-enum through a scalar
				// `mutable T&` out-param (`out <- (Ast::Expr.Foo(...))`) is region-polymorphic, exactly as
				// one that RETURNS such a node. Threading `__region_auto` makes its node builders allocate
				// into the CALLER's tree store; without it the node lands in a per-call arena freed on
				// return — a UAF once the caller stores the handle (the reverted quantifier-`where` bug).
				// Composes with an existing `__rg_parser` struct-region param (they thread DIFFERENT
				// regions — the tree store vs. the Parser's field arena), so no RegionParams gate needed.
				// Over-triggering is sound: a handle built in the caller ignores the threaded region.
				isPoly = returnsCallerArena || fnType.RegionPolymorphic || a.functionWritesHandleToScalarOutParam(fn)
			})
			if returnsCallerArena && !a.regionPolyReturnsCallerArena[fnType] {
				a.regionPolyReturnsCallerArena[fnType] = true
				changed = true
			}
			if isPoly && !fnType.RegionPolymorphic {
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
			if rs, isRegion := body[0].(*ast.RegionStmt); isRegion && isCompilerGeneratedAutoRegionStmt(rs) {
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
	var storeNeeds, signatureStoreNeeds map[*FuncType]map[string]*EnumType
	if len(regionBackedPacked) != 0 {
		storeNeeds, signatureStoreNeeds = a.computeTransitiveStoreNeeds(funcs, regionBackedPacked)
	} else {
		// No hierarchy can need a threaded store. Clear prior classification
		// state while retaining ordinary region-polymorphic parameter injection.
		a.packedStoreBuilders = map[*FuncType]map[string]string{}
	}
	// An export target must have NO implicit parameters (exports.go), so it can never receive a
	// threaded store — it owns a region and creates stores on demand. The generated test runner turns
	// each @test body into an export target (e.g. ctx_test_case_impl_0), so detect them structurally.
	exportTargets := map[string]bool{}
	for _, scoped := range decls {
		if ed, ok := scoped.Decl.(*ast.ExportFuncDecl); ok && ed != nil && ed.TargetName != "" {
			exportTargets[ed.TargetName] = true
		}
	}
	a.preBodyClassify = &retainedClassifyState{preparation: map[*ast.FuncDecl]retainedClassifyPreparation{}, storeNeeds: storeNeeds, regionBacked: regionBackedPacked, exportTarget: exportTargets}
	if a.retained != nil && a.retained.classify == nil && retainedFastPrepareMode != "" {
		a.retained.classify = a.preBodyClassify
	}
	for _, fn := range funcs {
		fnType := a.funcTypeForRegionPoly(fn)
		if fnType == nil {
			continue
		}
		a.preBodyClassify.preparation[fn] = retainedClassifyPreparation{
			RegionPolymorphic:  fnType.RegionPolymorphic,
			ReturnsCallerArena: a.regionPolyReturnsCallerArena[fnType],
			OwnsRegion:         a.funcOwnsRegion(fn), Entry: funcIsRegionStoreEntryPoint(fn), Export: exportTargets[fn.Name],
		}
		if len(regionBackedPacked) == 0 {
			if !exportTargets[fn.Name] && fnType.RegionPolymorphic {
				a.injectRegionPolymorphicParam(fnType)
			}
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
		decision := a.preBodyClassify.preparation[fn]
		decision.NeedsStoreHost = needsStoreHost
		a.preBodyClassify.preparation[fn] = decision
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
		if decision.Entry || decision.OwnsRegion {
			a.injectStoreNeeds(fnType, signatureStoreNeeds[fnType])
			if needsStoreHost {
				wrapFuncBodyInLazyAutoRegion(fn)
			}
		} else {
			a.injectStoreNeeds(fnType, storeNeeds[fnType])
		}
	}
	for _, fn := range funcs {
		decision, known := a.preBodyClassify.preparation[fn]
		if !known || len(fn.Body) != 1 {
			continue
		}
		switch body := fn.Body[0].(type) {
		case *ast.RegionStmt:
			if isCompilerGeneratedAutoRegionStmt(body) {
				decision.Wrapper = "auto-region"
			}
		case *ast.InStoreStmt:
			decision.Wrapper = "in-store"
		}
		a.preBodyClassify.preparation[fn] = decision
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
	walkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		switch n := node.(type) {
		case *ast.RegionStmt:
			if len(n.Body) != 0 {
				return false // scoped region hosts on-demand stores for its body
			}
		case *ast.InStoreStmt:
			return false // `in <owner>:` establishes an ambient owner
		case *ast.LambdaExpr:
			return false
		case *ast.CallExpr:
			if ft := a.regionPolyCalleeFuncType(n); ft != nil && len(storeNeeds[ft]) != 0 {
				found = true
				return false
			}
			if fieldExpr, ok := unwrapParenForRegionPoly(n.Func).(*ast.FieldExpr); ok {
				for _, ft := range a.regionPolyProtocolMethodImplFuncTypes(fieldExpr) {
					if len(storeNeeds[ft]) != 0 {
						found = true
						return false
					}
				}
			}
		}
		// A cast can resolve to a user `__cast__` hook — a call in disguise. This pre-pass
		// runs before bodies are analyzed (resolvedCastHooks is still empty), so detect the
		// site syntactically: if the cast TARGET type carries region-backed packed handles,
		// the hook (if any) carries the matching store param and this site synthesizes the
		// on-demand store just like a CallExpr.
		if castExpr, isCast := node.(*ast.CastExpr); isCast {
			if a.typeExprNeedsRegionBackedStore(castExpr.Target, regionBacked) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// collectRegionPolyCandidateFuncs flattens the top-level and impl-member function declarations into
// a single list for the classification fixpoint.
func collectRegionPolyCandidateFuncs(decls []scopedDecl) []*ast.FuncDecl {
	out, _ := collectRegionPolyCandidateFuncsScoped(decls)
	return out
}

// collectRegionPolyCandidateFuncsScoped is collectRegionPolyCandidateFuncs plus the enclosing
// module namespace of each function ("" for a top-level one).
//
// The namespace is NOT recoverable from the *ast.FuncDecl: flattening keeps a module member's
// Name bare ("stow") and carries "Box" alongside it on the scopedDecl. Anything that wants to
// match a function against the name written at a `::` call site needs both halves, because
// `Box::stow(...)` parses to Ident{Name: "Box.stow"} -- one dotted string, already joined.
func collectRegionPolyCandidateFuncsScoped(decls []scopedDecl) ([]*ast.FuncDecl, map[*ast.FuncDecl]string) {
	var out []*ast.FuncDecl
	namespaceOf := map[*ast.FuncDecl]string{}
	for _, scoped := range decls {
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			out = append(out, n)
			namespaceOf[n] = scoped.Namespace
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					out = append(out, fn)
					namespaceOf[fn] = scoped.Namespace
				}
			}
		}
	}
	return out, namespaceOf
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
