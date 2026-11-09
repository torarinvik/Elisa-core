package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"path/filepath"
	"strings"
)

func (a *Analyzer) validatePermissionUsage(decls []scopedDecl) {
	r := a.retained
	if r != nil && r.perms.diags == nil {
		r.perms.diags = map[*ast.FuncDecl][]Diagnostic{}
	}
	for _, scoped := range permissionFunctionDecls(decls) {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if r != nil && r.perms.replay(a, fn) {
			continue
		}
		from := len(a.diagnostics)
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			a.validateFunctionPermissionUsage(fn)
		})
		if r != nil {
			r.perms.diags[fn] = append([]Diagnostic(nil), a.diagnostics[from:]...)
		}
	}
}

func (a *Analyzer) validateFunctionPermissionUsage(fn *ast.FuncDecl) {
	// Ghost bodies are erased, so they need no local grants for runtime effects.
	if fn != nil && fn.IsGhost {
		return
	}
	granted := map[string]bool{}
	savedFuncType := a.currentFuncType
	defer func() { a.currentFuncType = savedFuncType }()
	if sym, ok := a.symbolForFuncDecl(fn); ok {
		if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
			a.currentFuncType = fnType
		}
	}
	a.withPermissionParams(fn.PermissionParams, func() {
		a.validateSegmentEntryAnnotations(fn)
		// docs/126 §3: a moved-in drop-typed parameter is released by THIS frame, at the
		// function's own grant level.
		a.validateImplicitDropGrants(fn, granted)
		a.validatePermissionStmts(fn.Body, granted)
		a.validateSegmentFlow(fn)
		if funcHasAnnotation(fn, "segment_agnostic") {
			a.validateSegmentAgnosticStmts(fn.Body)
		}
		if funcHasAnnotation(fn, "reentrant_safe") {
			a.validateReentrantSafeStmts(fn.Body)
		}
	})
}

func (a *Analyzer) validateSegmentEntryAnnotations(fn *ast.FuncDecl) {
	if fn == nil || !funcHasAnnotation(fn, "async_entry") {
		return
	}
	if !funcHasAnnotation(fn, "segment_agnostic") && !funcHasAnnotation(fn, "segment_establishing") {
		a.errorf(fn.Pos(), "@async_entry function %q enters with unknown segment owner; add @segment_agnostic or @segment_establishing so host/guest %%fs assumptions are explicit", fn.Name)
	}
	if !funcHasAnnotation(fn, "reentrant_safe") {
		a.errorf(fn.Pos(), "@async_entry function %q can interrupt arbitrary code; add @reentrant_safe so the async path is transitively lock-free/reentrant", fn.Name)
	}
}

func grantedPermissionFamilies(families []string) map[string]bool {
	granted := make(map[string]bool, len(families))
	for _, family := range families {
		granted[family] = true
	}
	return granted
}

func missingGrantedPermissionFamilies(refs []ast.PermissionRef, granted map[string]bool) []string {
	missingRefs := missingGrantedPermissionRefs(refs, granted)
	if len(missingRefs) == 0 {
		return nil
	}
	missing := make([]string, 0, len(missingRefs))
	seen := map[string]bool{}
	for _, ref := range missingRefs {
		if ref.Name == "" || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		missing = append(missing, ref.Name)
	}
	return missing
}

// errorOnMissingLocalGrant is warnOnMissingLocalGrant for operations whose Unsafe opt-out is
// mandatory in every mode (no permissive-mode warning).
func (a *Analyzer) errorOnMissingLocalGrant(pos lexer.Pos, label string, refs []ast.PermissionRef, granted map[string]bool) {
	missing := missingGrantedPermissionFamilies(refs, granted)
	if len(missing) == 0 {
		return
	}
	a.errorf(pos, "%s", effectAuthorityGrantMessage(label, missing, permissionGrantHint(refs, missing)))
}

func (a *Analyzer) warnOnMissingLocalGrant(pos lexer.Pos, label string, refs []ast.PermissionRef, granted map[string]bool) {
	missing := missingGrantedPermissionFamilies(refs, granted)
	if len(missing) == 0 {
		return
	}
	msg := effectAuthorityGrantMessage(label, missing, permissionGrantHint(refs, missing))
	// Teach the safe alternatives for the dynamic-index gate: most uses want a
	// checked access, not the unsafe opt-out. Appended after the standard message
	// so existing prefix assertions still hold.
	if label == "unchecked index" {
		msg += "; for a checked access write `get arr[i]` (propagates absence) or `arr[i] else <fallback>` (handles it inline), or keep it unchecked with `trusted Unsafe.UncheckedIndex`"
	}
	// Address-space crossings (Unsafe.GuestHostPointerCast) are the strongest
	// memory-safety boundary: converting a guest address carrier into a host
	// pointer is ALWAYS a hard error when ungranted, even when general unsafe
	// enforcement is in warning mode. This guarantees a produced or received
	// guest address can never be silently dereferenced as a host pointer; the
	// author must opt in with `trusted Unsafe.GuestHostPointerCast`.
	for _, ref := range missingGrantedPermissionRefs(refs, granted) {
		if ref.Name == "Unsafe" && ref.Member == "GuestHostPointerCast" {
			a.errorf(pos, "%s", msg)
			return
		}
	}
	// Memory-safety opt-outs (the Unsafe.* family) are HARD ERRORS under
	// enforcement: a memory-unsafe operation must not compile unless the author
	// explicitly grants the corresponding Unsafe permission (the "guaranteed
	// memory safety unless you explicitly opt out" contract, docs/26). Effect
	// authority (Abort/Memory/...) stays a warning.
	if a.enforceUnsafePermissions && containsUnsafeFamily(missing) {
		a.errorf(pos, "%s", msg)
		return
	}
	a.warnf(pos, msg)
}

// isRuntimeStdPermissionInternal identifies runtime source for specialized surface
// restrictions (for example, wrappers implementing raw concurrency primitives).
// It does not authorize effects or suppress inferred permission rows.
func isRuntimeStdPermissionInternal(path string) bool {
	// Real builds carry the full include path, so the parent directory identifies the whole
	// stdlib (every file, not a hand-maintained subset).
	if filepath.Base(filepath.Dir(path)) == "elisacore_std" {
		return true
	}
	// Fallback for contexts that carry an unqualified file name (test harnesses that load
	// the stdlib by bare name). Lists every elisacore_std source.
	return runtimeStdBaseNames[filepath.Base(path)]
}

// runtimeStdBaseNames identifies stdlib source files by bare name. Keep in sync
// with runtime/elisacore_std/*.elisa (the directory check above covers full-path builds).
var runtimeStdBaseNames = map[string]bool{
	"allocator.elisa": true, "arena.elisa": true,
	"arena_alloc_realloc.elisa": true, "arena_collection_stacks.elisa": true,
	"arena_defaults_and_pointers.elisa": true, "arena_format.elisa": true,
	"arena_lifecycle.elisa": true, "arena_region_blocks.elisa": true, "arena_region_cache.elisa": true,
	"collections.elisa": true, "debug_referee.elisa": true,
	"collections_inline_vec_flags.elisa": true, "collections_array_algorithms.elisa": true,
	"collections_hashing.elisa": true, "collections_dict.elisa": true,
	"collections_set.elisa": true, "collections_index_map.elisa": true,
	"deque.elisa": true, "elisacore_runtime.elisa": true, "elisacore_runtime_concurrency.elisa": true,
	"elisacore_runtime_prelude.elisa": true, "elisacore_runtime_strings.elisa": true,
	"elisacore_runtime_string_views.elisa": true, "elisacore_parse_float.elisa": true,
	"elisacore_parse_float_algorithms.elisa": true, "elisacore_regex.elisa": true,
	"elisacore_regex_automaton.elisa": true, "elisacore_regex_search.elisa": true,
	"elisacore_runtime_system_bridge.elisa": true, "heap.elisa": true, "names.elisa": true,
	"native_runtime_support.elisa": true, "runtime.elisa": true, "stores.elisa": true,
	"stores_core.elisa": true, "stores_packed_dense.elisa": true, "stores_packed_encoding.elisa": true,
	"stores_packed_sparse.elisa": true, "stores_rows.elisa": true, "stores_types.elisa": true,
	"test.elisa": true,
}

// containsUnsafeFamily keeps strict Unsafe enforcement independent of other
// advisory effect families present in the same operation or call.
func containsUnsafeFamily(families []string) bool {
	for _, family := range families {
		if family == "Unsafe" {
			return true
		}
	}
	return false
}

func (a *Analyzer) warnOnRedundantLocalGrant(pos lexer.Pos, label string, refs []ast.PermissionRef, granted map[string]bool) {
	refs = canonicalizePermissionRefs(refs)
	// Ambient permissions (region allocation) are granted everywhere by construction, so
	// declaring them is always technically redundant — but a legacy/explicit `can
	// Memory.Allocate` is harmless and must not warn. Only non-ambient grants can be redundant.
	refs = filterOutAmbientPermissionRefs(refs)
	if len(refs) == 0 {
		return
	}
	if len(missingGrantedPermissionRefs(refs, granted)) != 0 {
		return
	}
	hint := strings.TrimSpace(permissionGrantHint(refs, permissionFamiliesFromRefs(refs)))
	a.warnf(pos, "%s grants %s redundantly; a surrounding can ...: block already grants these effects", label, hint)
}

func (a *Analyzer) validatePermissionStmts(stmts []ast.Stmt, granted map[string]bool) {
	for _, stmt := range stmts {
		a.validatePermissionStmt(stmt, granted)
	}
}

func (a *Analyzer) validatePermissionStmt(stmt ast.Stmt, granted map[string]bool) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", a.mutableGlobalBorrowRefs[n], granted)
		if n.Value != nil {
			a.validatePermissionExpr(n.Value, granted)
		}
		// docs/126 §3: an implicitly dropped value charges its destructor's effects to
		// the scope that drops it. Checked at the DECLARATION because a drop always runs
		// in the declaring statement's own grant scope, so `granted` here is exactly the
		// set in force on every edge the drop can sit on.
		a.validateImplicitDropGrants(n, granted)
		if a.enforceUnsafePermissions && a.stmtRequiresUnsafeAlias(n) {
			a.warnOnMissingLocalGrant(n.Pos(), "mutable alias", unsafeAliasRefs(n.Position), granted)
		}
	case *ast.LetDestructureStmt:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.TupleBindStmt:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.MoveBindStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
	case *ast.DeferStmt:
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.AssignStmt:
		a.validatePermissionWriteTarget(n.Target, n.WriteThrough, granted)
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", a.mutableGlobalBorrowAssignRefs[n], granted)
		a.validatePermissionExpr(n.Value, granted)
		if a.enforceUnsafePermissions && a.stmtRequiresUnsafeAlias(n) {
			a.warnOnMissingLocalGrant(n.Pos(), "mutable alias", unsafeAliasRefs(n.Position), granted)
		}
	case *ast.AugAssignStmt:
		if n.CollectionAppend != nil {
			a.validatePermissionExpr(n.CollectionAppend, granted)
			return
		}
		a.validatePermissionWriteTarget(n.Target, true, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.AsRefAssignStmt:
		a.validatePermissionWriteTarget(n.Target, false, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.ReturnStmt:
		if a.currentFuncType != nil {
			a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", a.mutableGlobalReturnedRefRefs(n.Value, a.currentFuncType.Return), granted)
		}
		if n.Value != nil {
			a.validatePermissionExpr(n.Value, granted)
		}
	case *ast.IfStmt:
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionStmts(n.Then, cloneGrantedPermissionFamilies(granted))
		for _, elif := range n.Elifs {
			a.validatePermissionExpr(elif.Cond, granted)
			a.validatePermissionStmts(elif.Body, cloneGrantedPermissionFamilies(granted))
		}
		a.validatePermissionStmts(n.Else, cloneGrantedPermissionFamilies(granted))
	case *ast.MatchStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
		for _, arm := range n.Arms {
			a.validatePermissionExpr(arm.Guard, granted)
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.InStoreStmt:
		if root := globalReferenceStorageExpr(n.Store); root != nil {
			if _, global := a.mutableGlobalStorageRoot(root); global {
				a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", globalWriteRefs(root.Pos()), granted)
			}
		}
		a.validatePermissionExpr(n.Store, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.CanStmt:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		refs = a.addHandlerConcretePermissionRefs(n, refs)
		if n.SuppressPermissionInference {
			refs = trustedUnsafePermissionRefs(refs)
		}
		if !n.SuppressPermissionInference {
			a.warnOnRedundantLocalGrant(n.Pos(), "can block", refs, granted)
		}
		a.validatePermissionStmts(n.Body, a.extendGrantedPermissionRefs(granted, refs))
	case *ast.SignalStmt:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		a.warnOnMissingLocalGrant(n.Pos(), "signal", refs, granted)
	case *ast.LeakStmt:
		a.warnOnMissingLocalGrant(n.Pos(), "leak", unsafeLeakRefs(n.Position), granted)
	case *ast.PoolStmt:
		a.validatePermissionExpr(n.Workers, granted)
		if !granted["Pool"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Create"}, {Position: n.Position, Name: "Pool", Member: "Shutdown"}}
			a.warnOnMissingLocalGrant(n.Pos(), "pool scope", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.LockStmt:
		a.validatePermissionExpr(n.Mutex, granted)
		if !granted["Sync"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Sync", Member: "Lock"}, {Position: n.Position, Name: "Sync", Member: "Unlock"}}
			a.warnOnMissingLocalGrant(n.Pos(), "lock scope", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.WhileStmt:
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ForStmt:
		for _, preReserve := range ast.SynthesizedPreReserveStmts(n) {
			a.validatePermissionStmt(preReserve, granted)
		}
		a.validatePermissionExpr(n.Start, granted)
		a.validatePermissionExpr(n.End, granted)
		if n.Step != nil {
			a.validatePermissionExpr(n.Step, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.IterForStmt:
		for _, preReserve := range ast.SynthesizedPreReserveStmts(n) {
			a.validatePermissionStmt(preReserve, granted)
		}
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global read", a.mutableGlobalStoreRowsReadRefs(n.Source), granted)
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", a.mutableGlobalStoreRowsWriteRefs(n.Source), granted)
		a.validatePermissionExpr(n.Source, granted)
		a.validatePermissionExpr(n.WhereFilter, granted)
		a.validatePermissionExpr(n.Filter, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ParallelForStmt:
		a.validatePermissionExpr(n.Source, granted)
		if !granted["Pool"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Submit"}, {Position: n.Position, Name: "Pool", Member: "WaitAll"}}
			a.warnOnMissingLocalGrant(n.Pos(), "parallel for", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.PanicStmt:
		a.warnOnMissingLocalGrant(n.Pos(), "panic", []ast.PermissionRef{{Position: n.Position, Name: "Abort", Member: "Panic"}}, granted)
		a.validatePermissionExpr(n.Message, granted)
	case *ast.ExprStmt:
		a.validatePermissionExpr(n.Expr, granted)
	case *ast.StaticIfStmt:
		for _, active := range a.activeStmtBranch(n) {
			a.validatePermissionStmt(active, granted)
		}
	case *ast.StaticErrorStmt:
		a.validatePermissionExpr(n.Message, granted)
	case *ast.StaticAssertStmt:
		a.validatePermissionExpr(n.Cond, granted)
		if n.Message != nil {
			a.validatePermissionExpr(n.Message, granted)
		}
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			a.validatePermissionExpr(item.Cond, granted)
			if item.Message != nil {
				a.validatePermissionExpr(item.Message, granted)
			}
		}
	case *ast.StaticBlockStmt:
		for _, inner := range n.Body {
			a.validatePermissionStmt(inner, granted)
		}
	case *ast.DiscardStmt:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.RegionStmt:
		// Function/loop bodies with heap locals are auto-wrapped in a RegionStmt; without
		// this arm every permission check inside such a body was silently skipped.
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ScopeStmt:
		if n.Guard != nil {
			a.validatePermissionExpr(n.Guard, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	}
}

func (a *Analyzer) validatePermissionWriteTarget(expr ast.Expr, alsoRead bool, granted map[string]bool) {
	if expr == nil {
		return
	}
	if _, ok := a.mutableGlobalStorageRoot(expr); ok {
		a.errorOnMissingLocalGrant(expr.Pos(), "mutable global write", globalWriteRefs(expr.Pos()), granted)
		if a.globalPathLoadsMutableBorrow(expr) {
			a.errorOnMissingLocalGrant(expr.Pos(), "mutable global read", globalReadRefs(expr.Pos()), granted)
		}
		if alsoRead {
			a.errorOnMissingLocalGrant(expr.Pos(), "mutable global read", globalReadRefs(expr.Pos()), granted)
		}
	}
	if sym, ok := a.globalStorageRoot(expr); ok {
		if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
			a.warnOnMissingLocalGrant(expr.Pos(), "mutable global access", unsafeMutableGlobalRefs(expr.Pos()), granted)
		}
	}
	switch n := expr.(type) {
	case *ast.FieldExpr:
		a.validatePermissionTargetOperands(n.Object, granted)
	case *ast.IndexExpr:
		a.validatePermissionTargetOperands(n.Object, granted)
		a.validatePermissionExpr(n.Index, granted)
		a.validatePermissionExpr(n.Fallback, granted)
	case *ast.SliceExpr:
		a.validatePermissionTargetOperands(n.Object, granted)
		a.validatePermissionExpr(n.Start, granted)
		a.validatePermissionExpr(n.End, granted)
	case *ast.ParenExpr:
		a.validatePermissionTargetOperands(n.Inner, granted)
	}
}

// Walk subscript operands without treating the assignment's storage root as a read.
func (a *Analyzer) validatePermissionTargetOperands(expr ast.Expr, granted map[string]bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		a.validatePermissionTargetOperands(n.Object, granted)
	case *ast.IndexExpr:
		a.validatePermissionTargetOperands(n.Object, granted)
		a.validatePermissionExpr(n.Index, granted)
		a.validatePermissionExpr(n.Fallback, granted)
	case *ast.SliceExpr:
		a.validatePermissionTargetOperands(n.Object, granted)
		a.validatePermissionExpr(n.Start, granted)
		a.validatePermissionExpr(n.End, granted)
	case *ast.ParenExpr:
		a.validatePermissionTargetOperands(n.Inner, granted)
	}
}
