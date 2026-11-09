package semantic

import (
	"elisacore/src/ast"
)

// functionWritesHandleToScalarOutParam reports whether the function assigns to a by-reference PACKED
// handle-enum out-param (`def h(out: mutable Ast::Expr&): out <- (Ast::Expr.Foo(...))`). Any value
// assigned to such a ref IS a region-backed handle (the param type is the region-value gate), so if the
// assigned node was BUILT in this function it lives in the per-call arena and frees on return — a UAF
// once the caller stores the handle in a longer-lived node (the docs/125 quantifier `where`-clause bug).
// Detecting this shape classifies the function region-polymorphic (classifyRegionPolymorphicFunctions),
// so it threads `__region_auto` — its node builders then allocate into the CALLER's tree store, exactly
// as a function that RETURNS the node already does. Over-triggering is sound: if the assigned handle was
// built in the caller (already caller-region), the threaded region is simply unused for it. MUST run
// inside the candidate's resolution context so the qualified enum type resolves.
func (a *Analyzer) functionWritesHandleToScalarOutParam(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	assignsTo := func(name string) bool {
		found := false
		walkPruned(fn.Body, func(node any) bool {
			if found {
				return false
			}
			if asn, ok := node.(*ast.AssignStmt); ok && asn != nil {
				if id, ok := asn.Target.(*ast.Ident); ok && id != nil && id.Name == name {
					found = true
					return false
				}
			}
			return true
		})
		return found
	}
	for i := range fn.Params {
		p := &fn.Params[i]
		if a.refParamIsPackedEnum(p.Type) && assignsTo(p.Name) {
			return true
		}
	}
	return false
}

// regionValueTester builds the shared "does this expression produce a region-carrying value?" predicate
// used by both containerParamGrownWithRegionValue (inserted args) and scalarRefAssignedRegionValue
// (assigned out-param RHS). It first maps each local name to its producing expression (var-decl,
// assignment, or `if EXPR is x:` refinement binding) so an ident argument resolves to its producer, then
// returns a tester that classifies from RESOLVED TYPES only (callee return types, packed-enum
// constructors, owned-less list literals/comprehensions) — never region-poly classification, which runs
// after this pass. Conservative both ways: an unresolved value simply returns false.
func (a *Analyzer) regionValueTester(fn *ast.FuncDecl, funcByName map[string]*ast.FuncDecl, funcScopes map[*ast.FuncDecl]scopedDecl) func(ast.Expr) bool {
	// 1. Map each local name to the expression it was bound from, so an inserted ident resolves to its
	//    producer: a var-decl value, an assignment, or an `if EXPR is x:` refinement scrutinee.
	boundExpr := map[string]ast.Expr{}
	bind := func(n string, e ast.Expr) {
		if n == "" || e == nil {
			return
		}
		if _, seen := boundExpr[n]; !seen {
			boundExpr[n] = e
		}
	}
	bindRefinements := func(cond ast.Expr) {
		walkPruned(cond, func(node any) bool {
			if ob, ok := node.(*ast.OptionalBindExpr); ok && ob != nil {
				bind(ob.Name, ob.Value)
			}
			return true
		})
	}
	var collectBindings func(stmts []ast.Stmt)
	collectBindings = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				bind(s.Name, s.Value)
			case *ast.AssignStmt:
				if id, ok := s.Target.(*ast.Ident); ok && id != nil {
					bind(id.Name, s.Value)
				}
			case *ast.IfStmt:
				bindRefinements(s.Cond)
				collectBindings(s.Then)
				for _, elif := range s.Elifs {
					bindRefinements(elif.Cond)
					collectBindings(elif.Body)
				}
				collectBindings(s.Else)
			case *ast.WhileStmt:
				bindRefinements(s.Cond)
				collectBindings(s.Body)
			case *ast.ForStmt:
				collectBindings(s.Body)
			case *ast.IterForStmt:
				collectBindings(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					collectBindings(arm.Body)
				}
			case *ast.ScopeStmt:
				collectBindings(s.Body)
			case *ast.CanStmt:
				collectBindings(s.Body)
			case *ast.RegionStmt:
				collectBindings(s.Body)
			case *ast.InStoreStmt:
				collectBindings(s.Body)
			}
		}
	}
	collectBindings(fn.Body)
	resolveCalleeReturnType := func(callee *ast.FuncDecl) Type {
		if callee == nil || callee.ReturnType == nil {
			return invalidType
		}
		resolve := func() Type {
			// This is a conservative pre-classification probe. A malformed or
			// not-yet-bound generic return type must not emit a second diagnostic;
			// body analysis reports the authoritative error later.
			savedSuppress := a.suppressDiagnostics
			restoreReportProbe3 := a.beginProofReportProbe()
			defer restoreReportProbe3.restore()
			a.suppressDiagnostics = true
			resolved := a.resolveType(callee.ReturnType)
			a.suppressDiagnostics = savedSuppress
			restoreReportProbe3.restore()
			return resolved
		}
		if scoped, ok := funcScopes[callee]; ok {
			var resolved Type
			a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
				resolved = resolve()
			})
			return resolved
		}
		return resolve()
	}

	// 2. Decide whether an expression produces a region-carrying value (resolved types only).
	argIsRegionValued := func(arg ast.Expr) bool {
		visited := map[string]bool{}
		var rec func(e ast.Expr, depth int) bool
		rec = func(e ast.Expr, depth int) bool {
			if e == nil || depth > 16 {
				return false
			}
			switch n := unwrapParenForRegionPoly(e).(type) {
			case *ast.AllocExpr:
				return true
			case *ast.ListLitExpr:
				return n != nil && n.Owner == nil
			case *ast.ListComprehensionExpr:
				return n != nil && n.Owner == nil
			case *ast.Ident:
				if n == nil || visited[n.Name] {
					return false
				}
				visited[n.Name] = true
				if prod, ok := boundExpr[n.Name]; ok {
					return rec(prod, depth+1)
				}
				return false
			case *ast.CallExpr:
				// A bare constructor of a region-backed packed enum allocates region storage.
				if et, _, ok := a.packedAllocConstructorInfo(n); ok && et != nil {
					if et.Packed {
						return true
					}
					// An INLINE value-enum constructor (`Item.Row(make_vals(10))`) is region-valued
					// when any payload argument is — the variant embeds the payload directly, so the
					// wrapped container's backing must live in the grown param's region. Without this
					// the void-grower ambient adoption never fired for enum-wrapped inserts and the
					// payload backing was freed on return (silent UAF).
					for _, arg := range n.Args {
						if rec(arg, depth+1) {
							return true
						}
					}
					return false
				}
				// Otherwise resolve the callee's RETURN TYPE (region-carrying ⇒ region-valued).
				if cn, _, isCall := ast.PrepassCallShape(n); isCall {
					if callee, ok := funcByName[cn]; ok && callee != nil && callee.ReturnType != nil {
						return a.typeIsRegionValued(resolveCalleeReturnType(callee))
					}
				}
				return false
			case *ast.StructLitExpr:
				// Pre-analysis `Name(args)` parses as a StructLitExpr; resolve it as a call.
				if cn, _, isCall := ast.PrepassCallShape(n); isCall {
					if callee, ok := funcByName[cn]; ok && callee != nil && callee.ReturnType != nil {
						return a.typeIsRegionValued(resolveCalleeReturnType(callee))
					}
				}
				// A brace construction `Ty{field: value, …}` carries region storage when any
				// field initializer does (a struct wrapping a packed-enum handle or container —
				// e.g. `items.push(ParamDecl{type_expression: node})`): the wrapper struct is
				// copied into the caller's container but the wrapped handle's record (and its
				// side-table payloads) live in a region, which must therefore be the param's.
				if n != nil && n.Brace {
					for _, fieldValue := range n.Args {
						if rec(fieldValue, depth+1) {
							return true
						}
					}
					for _, spread := range n.Spreads {
						if rec(spread, depth+1) {
							return true
						}
					}
				}
				return false
			}
			return false
		}
		return rec(arg, 0)
	}
	return argIsRegionValued
}

// typeIsRegionValued reports whether a value of this resolved type owns/references region-backed
// storage that must be adopted with it: a container/view/store-struct (typeCarriesRegionStorage), OR
// a region-backed packed-enum HANDLE — whose record lives in a region store and whose variant
// payloads may hold side-table containers (docs/76). The packed-enum case is NOT folded into the
// shared typeCarriesRegionStorage because that predicate gates escape checks with narrower intent;
// here we only decide whether inserting such a value into a caller-owned container forces the
// container's region to be threaded.
func (a *Analyzer) typeIsRegionValued(t Type) bool {
	if typeCarriesRegionStorage(t) {
		return true
	}
	for t != nil {
		switch tt := t.(type) {
		case *OptionalType:
			if tt == nil {
				return false
			}
			t = tt.Value
		case *AggregateStateType:
			if tt == nil {
				return false
			}
			t = tt.Base
		case *RefType:
			if tt == nil {
				return false
			}
			t = tt.Elem
		case *EnumType:
			return tt != nil && (tt.Packed || tt.Root().Packed)
		default:
			return false
		}
	}
	return false
}

// collectProgramLifetimeRoots gathers the names that, when used as an `in <name>:` allocation
// scope, denote program-lifetime (never-freed) storage: the implicit `perm` arena plus every
// global / extern variable (e.g. `global mutable elf_parse_arena: Arena`). A container growth
// lexically inside such a scope needs no threaded caller region — its backing outlives all callers.
func collectProgramLifetimeRoots(decls []scopedDecl) map[string]bool {
	roots := map[string]bool{"perm": true}
	for _, scoped := range decls {
		switch d := scoped.Decl.(type) {
		case *ast.GlobalDecl:
			if d != nil && d.Name != "" {
				roots[d.Name] = true
			}
		case *ast.ExternVarDecl:
			if d != nil && d.Name != "" {
				roots[d.Name] = true
			}
		case *ast.ExportGlobalDecl:
			if d != nil && d.Alias != "" {
				roots[d.Alias] = true
			}
		}
	}
	return roots
}

// storeRegionRootName returns the root identifier name of an `in <store>:` allocation expression,
// peeling `&`, parens, and reborrow casts — so `in &elf_parse_arena:` yields "elf_parse_arena"
// and `in perm:` yields "perm". Returns "" for any shape without a clear root ident.
func storeRegionRootName(store ast.Expr) string {
	for {
		switch e := store.(type) {
		case *ast.ParenExpr:
			store = e.Inner
		case *ast.AddrOfExpr:
			store = e.Operand
		case *ast.CastExpr:
			store = e.Operand
		default:
			if id := rootIdentExpr(store); id != nil {
				return id.Name
			}
			return ""
		}
	}
}

// paramContainerIsGrownNeedingRegion is the region-inference-facing variant of
// paramContainerIsGrown: it reports whether the param's container is grown in a way that REQUIRES
// threading the caller's region — i.e. a growth that is NOT lexically inside a program-lifetime
// `in perm:` / `in &<global>:` scope. A growth whose allocation region is program-lifetime is
// skipped (its backing outlives every caller, so no region param is needed); a growth into a
// scoped/local arena, or with no enclosing region scope, still counts (preserving the prior
// "cannot infer region parameter" / escape behaviour — soundness unchanged). This is the missing
// complement to paramFieldContainerIsGrown's scope-skip, but restricted to PROGRAM-LIFETIME scopes
// so a scoped-arena growth is never silently accepted.
func paramContainerIsGrownNeedingRegion(stmts []ast.Stmt, name string, permRoots map[string]bool) bool {
	found := false
	walkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		// A growth inside an `in <programLifetimeArena>:` scope already has a program-lifetime
		// allocation region supplied lexically — don't descend (it needs no threaded region).
		if in, ok := node.(*ast.InStoreStmt); ok && in != nil {
			if permRoots[storeRegionRootName(in.Store)] {
				return false
			}
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && containerGrowthMethods[field.Field] {
				if id, ok := field.Object.(*ast.Ident); ok && id != nil && id.Name == name {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// paramContainerReassignedFromLiteral reports whether the body REASSIGNS the named ref-container
// parameter from a freshly-allocating literal — `param <- [a, b]`, `param <- [x for x in ...]`, or
// `param <- "..."` (the dstr string-literal sugar, still a StringLit at this pre-pass; flow analysis
// later rewrites it to a byte-list literal). Like push, whole-container reassignment from a literal
// allocates a new backing buffer that must land in the param's region — so it equally forces the
// param to be region-polymorphic, allocating into the caller's region (sound) rather than a local
// auto-region (which would dangle through the ref — use-after-free). Only literal RHS forms qualify;
// reassignment from a call/other container raises region-compatibility questions push never had, so
// it is conservatively excluded (a false negative just reinstates the old "active scope" error).
func paramContainerReassignedFromLiteral(stmts []ast.Stmt, name string) bool {
	found := false
	walkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		if asn, ok := node.(*ast.AssignStmt); ok && asn != nil && !asn.Optional {
			if id, ok := asn.Target.(*ast.Ident); ok && id != nil && id.Name == name && reassignmentAllocatesFreshBacking(asn.Value) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// reassignmentAllocatesFreshBacking reports whether an assignment RHS allocates a new container
// backing buffer in place: a list/dict/set literal, a comprehension, or a string literal (the dstr
// sugar, rewritten to a byte-list literal in flow analysis). Parens are unwrapped.
func reassignmentAllocatesFreshBacking(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch value.(type) {
	case *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.StringLit:
		return true
	}
	return false
}

// paramStructIsProgramLived: docs/128 -- a by-ref struct parameter whose every field is
// program-lived (`@source`) needs no inferred region: its growable fields already allocate in
// the program region, which outlives any region a caller could thread.
// paramStructIsProgramLivedIn resolves typ in the function's own namespace (a struct declared
// in a module is unknown by its bare name outside it).
func (a *Analyzer) paramStructIsProgramLivedIn(scoped scopedDecl, typ ast.TypeExpr) bool {
	result := false
	a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
		result = a.paramStructIsProgramLived(typ)
	})
	return result
}

func (a *Analyzer) paramStructIsProgramLived(typ ast.TypeExpr) bool {
	if a == nil || len(a.programRegions) == 0 || typ == nil {
		return false
	}
	// Silent: this runs before every type is registered; an unresolved name simply keeps the
	// inferred region (the conservative answer).
	saved := a.suppressDiagnostics
	restoreReportProbe4 := a.beginProofReportProbe()
	defer restoreReportProbe4.restore()
	a.suppressDiagnostics = true
	resolved := a.resolveType(typ)
	a.suppressDiagnostics = saved
	restoreReportProbe4.restore()
	if resolved == nil {
		return false
	}
	elem := stripRefForBounds(resolved)
	if _, isStruct := elem.(*StructType); !isStruct {
		return false
	}
	return a.typeCanContainRegionRefs(elem, map[string]bool{}) && a.typeIsProgramLivedCached(elem)
}

// paramStructHasExplicitBackingIn avoids assigning the descriptor slot's borrow
// lifetime to buffers that already name their own allocation region. Unknown or
// indirect field shapes retain the existing conservative inference.
func (a *Analyzer) paramStructHasExplicitBackingIn(scoped scopedDecl, fn *ast.FuncDecl, typ ast.TypeExpr) bool {
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
	if fn == nil || len(fn.TypeParams) != 0 || len(fn.PermissionParams) != 0 {
		return false
	}
	result := false
	saved := a.suppressDiagnostics
	a.suppressDiagnostics = true
	defer func() { a.suppressDiagnostics = saved }()
	a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
		a.withRegionParams(fn.RegionParams, func() {
			resolved := stripRefForBounds(a.resolveType(typ))
			if memo := a.explicitBackingMemo; memo != nil {
				if known, seen := memo.results[resolved]; seen {
					result = known
					return
				}
				defer func() { memo.results[resolved] = result }()
			}
			fields, ok := a.resolvedStructFields(resolved)
			if !ok || len(fields) == 0 {
				return
			}
			hasBacking := false
			for _, field := range fields {
				switch t := field.Type.(type) {
				case *BuiltinType, *BitIntType:
				case *DArrayType, *DictType, *SetType, *CStrType, *SViewType, *ViewType:
					if containerRegion(t) == "" {
						return
					}
					hasBacking = true
				default:
					return
				}
			}
			result = hasBacking
		})
	})
	return result
}

// regionInferenceCallers maps each candidate to the candidates whose bodies name it, through any
// expression funcByName resolves: a call shape or a bare function reference.
func regionInferenceCallers(cands []*ast.FuncDecl, funcByName map[string]*ast.FuncDecl) map[*ast.FuncDecl][]*ast.FuncDecl {
	callers := map[*ast.FuncDecl][]*ast.FuncDecl{}
	for _, fn := range cands {
		if fn == nil || len(fn.Body) == 0 {
			continue
		}
		seen := map[*ast.FuncDecl]bool{}
		note := func(name string) {
			if callee := funcByName[name]; callee != nil && !seen[callee] {
				seen[callee] = true
				callers[callee] = append(callers[callee], fn)
			}
		}
		walkStoreScanNodes(fn.Body, func(node any) {
			switch n := node.(type) {
			case *ast.Ident:
				note(n.Name)
			case ast.Expr:
				if name, _, ok := ast.PrepassCallShape(n); ok {
					note(name)
				}
			}
		})
	}
	return callers
}
