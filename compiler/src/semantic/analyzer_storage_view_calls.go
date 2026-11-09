package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func (a *Analyzer) storageViewDependencyForCall(call *ast.CallExpr) (storageViewDependencyState, bool) {
	if call == nil {
		return storageViewDependencyState{}, false
	}
	name := callBaseName(call)
	if base := overloadedDictHelperName(name); base != "" {
		name = base
	}
	switch name {
	case "bytes_view", "bytes_view_range", "bytes_view_range_ref":
		if len(call.Args) >= 1 {
			// The view addresses the argument's buffer directly (like `as_sview`), so a callee
			// later given that storage by mutable ref -- a whole-struct `mutable P&` that may grow
			// `p.source` -- dangles it: Interior (storageViewCalleeWriteSpare still spares fields
			// the callee cannot write).
			dep, ok := storageViewDependencyFromSource(call.Args[0])
			dep.Interior = ok
			return dep, ok
		}
		return storageViewDependencyState{}, false
	case "sview", "string_view_slice", "string_view_prefix", "string_view_suffix":
		if len(call.Args) >= 1 {
			return a.storageViewDependencyForBorrowedExpr(call.Args[0])
		}
		return storageViewDependencyState{}, false
	case "arena_dict_get", "arena_dict_get_mut", "arena_dict_get_cstr_view", "arena_dict_get_cstr_view_mut":
		// arena_dict_get returns `&items[i].value` — an interior reference into the dict's bucket
		// array. A later relocating insert (arena_dict_put*/get_or_insert resize → realloc) moves
		// that array, dangling the reference. Record a dependency on the dict so the insert
		// invalidates it (the dict analogue of darray interior-ref-after-push).
		if len(call.Args) >= 1 {
			return storageViewDependencyFromSource(dictContainerArgBase(call.Args[0]))
		}
		return storageViewDependencyState{}, false
	case "arena_dict_put", "arena_dict_put_checked", "arena_dict_put_or_panic", "arena_dict_get_or_insert", "arena_dict_get_or_insert_checked", "arena_dict_get_or_insert_or_panic":
		// These also return an interior ref into the (post-resize) bucket array — depends on the
		// dict (arg 1, after the Arena) so a subsequent insert invalidates it.
		if len(call.Args) >= 2 {
			return storageViewDependencyFromSource(dictContainerArgBase(call.Args[1]))
		}
		return storageViewDependencyState{}, false
	case "slice":
		// slice(&da) borrows the darray's backing as a Slice[T]. A later relocating
		// mutation of `da` (push/resize/reserve/clear/truncate) moves that backing and
		// dangles the slice, so record a dependency on the source darray. Using the slice
		// after such a mutation is then flagged, the same as an interior-ref-after-push.
		// Gated to the borrow form `slice(&x)` (one AddrOf argument) so an unrelated
		// function named `slice` taking a value does not spuriously record a borrow.
		if len(call.Args) == 1 {
			if addr, ok := stripOptimizationParens(call.Args[0]).(*ast.AddrOfExpr); ok && addr.Operand != nil {
				return storageViewDependencyFromSource(addr.Operand)
			}
			// A source that is already a reference (`slice(xs)` where `xs: darray[T]&`, the
			// `by par` desugar's form) borrows the same backing as `slice(&xs)`.
			if ident, ok := stripOptimizationParens(call.Args[0]).(*ast.Ident); ok && ident != nil {
				return storageViewDependencyFromSource(ident)
			}
		}
		return storageViewDependencyState{}, false
	case "read_view":
		// read_view(&da) borrows the darray's backing as a read-only ReadView[T].
		// Same relocation hazard as slice(&da): a relocating mutation of `da` moves the
		// backing and dangles the view. Record a dependency so any such mutation while
		// the view is live is a stale-reference compile error.
		if len(call.Args) == 1 {
			if addr, ok := stripOptimizationParens(call.Args[0]).(*ast.AddrOfExpr); ok && addr.Operand != nil {
				return storageViewDependencyFromSource(addr.Operand)
			}
			// A source that is already a reference (`read_view(xs)` where `xs: darray[T]&`, the
			// `by par` desugar's form) borrows the same backing as `read_view(&xs)`.
			if ident, ok := stripOptimizationParens(call.Args[0]).(*ast.Ident); ok && ident != nil {
				return storageViewDependencyFromSource(ident)
			}
		}
		return storageViewDependencyState{}, false
	case "split":
		// A band borrows whatever its source slice borrows: propagate the dependency so
		// `split(whole, ...)` over a sliced darray inherits the darray dependency.
		if len(call.Args) >= 1 {
			return a.storageViewDependencyForExpr(call.Args[0])
		}
		return storageViewDependencyState{}, false
	case "darray_view", "arena_da_view":
		if len(call.Args) == 0 {
			return storageViewDependencyState{}, false
		}
		return storageViewDependencyFromSource(call.Args[0])
	case "readonly", "arena_da_view_slice", "arena_da_view_prefix", "arena_da_view_suffix":
		if len(call.Args) == 0 {
			return storageViewDependencyState{}, false
		}
		return a.storageViewDependencyForBorrowedExpr(call.Args[0])
	}
	// An enum variant construction (`Tok.Word(v)`) carries its payloads' borrows: the
	// enum value stays dependent on whatever storage a payload view points into, so a
	// later relocation of that storage must invalidate the enum value too.
	if a.isEnumVariantConstructorCall(call) {
		var deps []storageViewDependencyState
		for _, argument := range call.Args {
			if dep, ok := a.storageViewDependencyForExpr(argument); ok {
				deps = append(deps, dep)
			}
		}
		return mergeStorageViewDependencies(deps...)
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && field.Field == "view" && field.Object != nil {
		return storageViewDependencyFromSource(field.Object)
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && field.Object != nil {
		switch field.Field {
		case "as_sview", "as_cstr":
			// The view addresses the receiver's buffer directly, so a callee given the
			// receiver by mutable ref (which may reassign or grow it) dangles it: Interior.
			dep, ok := storageViewDependencyFromSource(field.Object)
			dep.Interior = ok
			return dep, ok
		}
	}
	return a.storageViewDependencyForUserCall(call)
}

// storageViewDependencyForUserCall: a user function whose result may retain borrowed
// storage (`def name(self: Holder&) -> sview: return bytes_view_range(self.buf, ...)`,
// `first[T](xs: darray[T]&) -> T` over a darray of views) depends on whatever the
// arguments its return-borrow summary names depend on. Without this, a view returned
// by a helper survived a later push/replacement of the argument's storage.
func (a *Analyzer) storageViewDependencyForUserCall(call *ast.CallExpr) (storageViewDependencyState, bool) {
	if a.currentStorageViewDeps == nil && a.currentScope == nil {
		return storageViewDependencyState{}, false
	}
	resultType := a.exprType(call)
	// A thread handle owns the closure it runs: the spawned body reads every view the
	// closure captured, so the handle depends on them until joined.
	if resultType != nil && strings.HasPrefix(resultType.String(), "Thread[") {
		var deps []storageViewDependencyState
		for _, argument := range call.Args {
			if lambda, isLambda := stripOptimizationParens(argument).(*ast.LambdaExpr); isLambda && lambda != nil {
				if dep, ok := a.storageViewDependencyForExpr(lambda); ok {
					deps = append(deps, dep)
				}
			}
		}
		return mergeStorageViewDependencies(deps...)
	}
	if !storageViewTopLevelBorrowType(resultType) {
		// An enum result can carry a payload view into an argument (`wrap(b) -> Tok`
		// returning `Tok.Word(b.as_sview())`); trace it through the return summary.
		if _, isEnum := resultType.(*EnumType); !isEnum || !a.typeCarriesBorrowedStorage(resultType, map[Type]bool{}) {
			return storageViewDependencyState{}, false
		}
	}
	// A reference to a container HEADER (`-> mutable darray[T]&` returning `&state.items`)
	// survives the container's own growth; its relocation hazard is tracked as a container
	// alias, not as a buffer dependency.
	if ref, isRef := resultType.(*RefType); isRef && ref != nil {
		switch stripStorageViewRefs(ref.Elem).(type) {
		case *DArrayType, *DictType, *SetType:
			return storageViewDependencyState{}, false
		}
		if b, isBuiltin := stripStorageViewRefs(ref.Elem).(*BuiltinType); isBuiltin && b != nil && b.Name == "dstr" {
			return storageViewDependencyState{}, false
		}
	}
	// Trace which argument field paths the callee's returns borrow from (a syntactic
	// summary); an untraceable callee records nothing rather than every argument.
	decls, args, ok := a.storageViewOriginCallee(call)
	if !ok {
		return storageViewDependencyState{}, false
	}
	summary := a.storageViewReturnOriginsForAll(decls, 0)
	if !summary.Known {
		return storageViewDependencyState{}, false
	}
	var deps []storageViewDependencyState
	for _, origin := range summary.Origins {
		if origin.Param < 0 || origin.Param >= len(args) || args[origin.Param] == nil {
			continue
		}
		arg := stripOptimizationParens(args[origin.Param])
		passed := arg
		if addr, isAddr := arg.(*ast.AddrOfExpr); isAddr && addr != nil && addr.Operand != nil {
			arg = stripOptimizationParens(addr.Operand)
		}
		if !origin.Into {
			// The callee returns the argument itself (`idf(x: T&) -> T&: return x`), so the
			// result borrows whatever the PASSED expression borrows -- for `idf(&buf[0])` an
			// interior reference into buf's relocatable buffer, not the scalar `buf[0]`.
			if dep, ok := a.storageViewDependencyForExpr(passed); ok {
				deps = append(deps, dep)
			} else if passed != arg {
				if dep, ok := a.storageViewDependencyForExpr(arg); ok {
					deps = append(deps, dep)
				}
			}
			continue
		}
		if origin.Suffix == "" {
			if storageViewScalarBindingType(stripStorageViewRefs(a.exprType(arg))) {
				continue
			}
			if dep, ok := a.storageViewDependencyForExpr(arg); ok {
				deps = append(deps, dep)
			}
			// Only an argument that OWNS relocatable storage can have the result's backing
			// moved by a later mutation; arena handles hand out stable addresses.
			if !a.storageViewTypeOwnsRelocatable(stripStorageViewRefs(a.exprType(arg)), map[Type]bool{}) {
				continue
			}
		}
		// An `@append_only` store never moves bytes a view points into (its make_room
		// retires a full buffer instead of growing it, which the store checker enforces),
		// so the view does not depend on later borrows of the store.
		if storageViewPathCrossesAppendOnly(a.exprType(arg), origin.Suffix) {
			continue
		}
		key := optimizationExprString(arg)
		if key == "" {
			continue
		}
		// An "into" origin is an address into the argument's own relocatable storage, so a
		// callee later handed that storage by `mutable T&` (which may grow, clear or reassign
		// it) ends the buffer the result points into: Interior. A whole-struct borrow ends it
		// only when that callee may write the field it lies in (storageViewCalleeWriteSpare).
		deps = append(deps, storageViewDependencyState{Sources: []string{key + origin.Suffix}, Valid: true, Interior: true})
	}
	return mergeStorageViewDependencies(deps...)
}

func (a *Analyzer) storageViewDependencyForBorrowedExpr(expr ast.Expr) (storageViewDependencyState, bool) {
	if dependency, ok := a.storageViewDependencyForExpr(expr); ok && len(dependency.Sources) > 0 {
		return dependency, true
	}
	return storageViewDependencyFromSource(expr)
}

// callBaseName returns a call's function name for both plain `f(...)` and specialized `f[T](...)`
// (SpecializeExpr) call forms — callIdentName only handles the plain form.
func callBaseName(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	switch f := call.Func.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SpecializeExpr:
		if id, ok := f.Operand.(*ast.Ident); ok && id != nil {
			return id.Name
		}
	}
	return ""
}

// overloadedDictHelperName recovers the runtime dict helper behind an overload-resolved callee
// (`__ovl__arena_dict_get__mutable_dict_K_T__arena_dict_get`): the method form `m.get(k)` /
// `m.put(k, v)` is rewritten to the arena_dict_* helper and then overload-mangled, so a plain
// name switch misses it and the interior reference escapes the dict-insert invalidation.
func overloadedDictHelperName(name string) string {
	const prefix = "__ovl__"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	visible := strings.TrimPrefix(name, prefix)
	if i := strings.Index(visible, "__"); i >= 0 {
		visible = visible[:i]
	}
	if !strings.HasPrefix(visible, "arena_dict_") {
		return ""
	}
	return visible
}

// dictContainerArgBase peels a dict argument (`m.ref[mutable dict&]` = cast of &m, or a bare
// `m`) down to the underlying container lvalue, so the dependency source key matches between the
// borrow-producing get and the invalidating insert.
func dictContainerArgBase(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.AddrOfExpr:
			expr = n.Operand
		default:
			return expr
		}
	}
}

// The invalidation is a REPLACEMENT: a rehash re-slots every entry inside the bucket array even
// when a reserve_commit backing keeps its base fixed, so a stable backing cannot excuse a stale
// value reference (it would read whichever entry now occupies the old slot).
//
// invalidateStorageViewsForRelocatingDictCall invalidates interior references into a dict when a
// relocating insert (arena_dict_put/put_checked/get_or_insert, which can resize → realloc the
// bucket array) is performed on it — so a stale `arena_dict_get` reference used afterward is a
// stale-ref error rather than a silent use-after-realloc.
func (a *Analyzer) invalidateStorageViewsForRelocatingDictCall(call *ast.CallExpr) {
	if call == nil {
		return
	}
	name := callBaseName(call)
	if base := overloadedDictHelperName(name); base != "" {
		name = base
	}
	// The value-returning method form (`m <- m.put(k, v)`) stays a method call on the dict
	// receiver; it inserts into (and may rehash) the same bucket array.
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && name == "" {
		switch field.Field {
		case "put", "put_checked", "put_or_panic", "get_or_insert", "get_or_insert_checked", "get_or_insert_or_panic", "reserve":
			if _, isDict := stripRefForBounds(a.exprType(field.Object)).(*DictType); isDict {
				base := dictContainerArgBase(field.Object)
				a.invalidateStorageViewsForSourceMode(base, "dict insert (may rehash and relocate the bucket array)", true)
				a.invalidateIndexBoundsForContainer(base)
			}
		}
		return
	}
	switch name {
	case "arena_dict_put", "arena_dict_put_checked", "arena_dict_put_or_panic", "arena_dict_get_or_insert", "arena_dict_get_or_insert_checked", "arena_dict_get_or_insert_or_panic":
	default:
		return
	}
	if len(call.Args) < 2 {
		return
	}
	base := dictContainerArgBase(call.Args[1])
	a.invalidateStorageViewsForSourceMode(base, "dict insert (may rehash and relocate the bucket array)", true)
	a.invalidateIndexBoundsForContainer(base)
}

func storageViewDependencyFromSource(source ast.Expr) (storageViewDependencyState, bool) {
	key := optimizationExprString(source)
	if key == "" {
		return storageViewDependencyState{}, false
	}
	return storageViewDependencyState{Sources: []string{key}, Valid: true}, true
}

func mergeStorageViewDependencies(dependencies ...storageViewDependencyState) (storageViewDependencyState, bool) {
	merged := storageViewDependencyState{Valid: true}
	seen := make(map[string]bool)
	seenAliases := make(map[string]bool)
	for _, dependency := range dependencies {
		for _, source := range dependency.Sources {
			if source != "" && !seen[source] {
				seen[source] = true
				merged.Sources = append(merged.Sources, source)
			}
		}
		for _, alias := range dependency.ContainerAliases {
			if alias != "" && !seenAliases[alias] {
				seenAliases[alias] = true
				merged.ContainerAliases = append(merged.ContainerAliases, alias)
			}
		}
		merged.Interior = merged.Interior || dependency.Interior
		if !dependency.Valid {
			merged.Valid = false
			if merged.InvalidatedBy == "" {
				merged.InvalidatedBy = dependency.InvalidatedBy
			}
			merged.Replaced = merged.Replaced || dependency.Replaced
		}
	}
	if len(merged.Sources) == 0 {
		return storageViewDependencyState{}, false
	}
	return merged, true
}
