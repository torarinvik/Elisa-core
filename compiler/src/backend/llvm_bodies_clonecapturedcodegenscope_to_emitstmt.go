//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"reflect"
	"strings"
)

func cloneCapturedCodegenScope(scope *codegenScope) *codegenScope {
	if scope == nil {
		return nil
	}
	return &codegenScope{
		bindingName:              scope.bindingName,
		binding:                  scope.binding,
		bindings:                 cloneValueBindingMap(scope.bindings),
		packedCommonValueName:    scope.packedCommonValueName,
		packedCommonValueBinding: scope.packedCommonValueBinding,
		packedCommonValues:       clonePackedCommonBindingMap(scope.packedCommonValues),
		packedEnumPtrs:           clonePackedEnumStorageBindingMap(scope.packedEnumPtrs),
		packedEnumStoreName:      scope.packedEnumStoreName,
		packedEnumStoreBinding:   scope.packedEnumStoreBinding,
		packedEnumStores:         clonePackedStoreBindingMap(scope.packedEnumStores),
		packedViewName:           scope.packedViewName,
		packedViewBinding:        scope.packedViewBinding,
		packedViewPtrs:           clonePackedViewBindingMap(scope.packedViewPtrs),
	}
}
func capturePrefixMatches(root string, key string) bool {
	if root == "" || key == "" {
		return false
	}
	return key == root || strings.HasPrefix(key, root+".") || strings.HasPrefix(key, root+"[")
}
func (s *functionState) captureDeferFunctionScope(stmt *ast.DeferStmt) (*codegenScope, error) {
	if s == nil || stmt == nil || s.g == nil || s.g.result == nil {
		return nil, nil
	}
	info := s.g.semanticReadView().ValueDefer(stmt)
	if info == nil || len(info.Captures) == 0 {
		return nil, nil
	}
	captured := &codegenScope{}
	for _, name := range info.Captures {
		binding, ok := s.lookupBinding(name)
		if !ok {
			return nil, fmt.Errorf("missing defer capture binding %q during LLVM lowering", name)
		}
		defineBindingInCodegenScope(captured, name, binding)
	}
	for _, root := range info.Captures {
		for scope := s.scope; scope != nil; scope = scope.parent {
			if key := scope.packedCommonValueName; capturePrefixMatches(root, key) {
				bindPackedCommonFieldValueInCodegenScope(captured, key, scope.packedCommonValueBinding)
			}
			for key, binding := range scope.packedCommonValues {
				if capturePrefixMatches(root, key) {
					bindPackedCommonFieldValueInCodegenScope(captured, key, binding)
				}
			}
			for key, binding := range scope.packedEnumPtrs {
				if capturePrefixMatches(root, key) {
					bindPackedEnumStorageInCodegenScope(captured, key, binding)
				}
			}
			if key := scope.packedEnumStoreName; capturePrefixMatches(root, key) {
				bindPackedEnumStoreInCodegenScope(captured, key, scope.packedEnumStoreBinding)
			}
			for key, binding := range scope.packedEnumStores {
				if capturePrefixMatches(root, key) {
					bindPackedEnumStoreInCodegenScope(captured, key, binding)
				}
			}
			if key := scope.packedViewName; capturePrefixMatches(root, key) {
				bindPackedViewInCodegenScope(captured, key, scope.packedViewBinding)
			}
			for key, binding := range scope.packedViewPtrs {
				if capturePrefixMatches(root, key) {
					bindPackedViewInCodegenScope(captured, key, binding)
				}
			}
		}
	}
	return cloneCapturedCodegenScope(captured), nil
}
func (s *functionState) injectCapturedScope(captured *codegenScope) {
	if s == nil || s.scope == nil || captured == nil {
		return
	}
	if captured.bindingName != "" {
		defineBindingInCodegenScope(s.scope, captured.bindingName, captured.binding)
	}
	for name, binding := range captured.bindings {
		defineBindingInCodegenScope(s.scope, name, binding)
	}
	if captured.packedCommonValueName != "" {
		bindPackedCommonFieldValueInCodegenScope(s.scope, captured.packedCommonValueName, captured.packedCommonValueBinding)
	}
	for name, binding := range captured.packedCommonValues {
		bindPackedCommonFieldValueInCodegenScope(s.scope, name, binding)
	}
	for name, binding := range captured.packedEnumPtrs {
		bindPackedEnumStorageInCodegenScope(s.scope, name, binding)
	}
	if captured.packedEnumStoreName != "" {
		bindPackedEnumStoreInCodegenScope(s.scope, captured.packedEnumStoreName, captured.packedEnumStoreBinding)
	}
	for name, binding := range captured.packedEnumStores {
		bindPackedEnumStoreInCodegenScope(s.scope, name, binding)
	}
	if captured.packedViewName != "" {
		bindPackedViewInCodegenScope(s.scope, captured.packedViewName, captured.packedViewBinding)
	}
	for name, binding := range captured.packedViewPtrs {
		bindPackedViewInCodegenScope(s.scope, name, binding)
	}
}
func (s *functionState) registerScopedCleanup(binding scopedCleanupBinding) {
	binding.owner = s.scope
	s.scopedCleanups = append(s.scopedCleanups, binding)
}
func (s *functionState) registerFunctionCleanup(binding scopedCleanupBinding) {
	binding.owner = nil
	s.scopedCleanups = append(s.scopedCleanups, binding)
}

// markRegionScopeOwned removes a scoped region from the function-return cleanup set. A region
// entered with `in <arena>:` is released by its scope cleanup on every normal and abrupt exit;
// retaining it in s.regions as function-owned emitted a second arena_free on the function return.
// Loop-reset regions are deliberately kept function-owned because their scope cleanup resets them
// for reuse and the function exit must perform the final free.
func (s *functionState) markRegionScopeOwned(ptr llvmValueRef) {
	if s == nil || ptr == nil {
		return
	}
	for i := len(s.regions) - 1; i >= 0; i-- {
		if s.regions[i].ptr == ptr {
			s.regions[i].owned = false
			return
		}
	}
}
func (s *functionState) discardScopeCleanups(scope *codegenScope) {
	if scope == nil || len(s.scopedCleanups) == 0 {
		return
	}
	out := s.scopedCleanups[:0]
	for _, binding := range s.scopedCleanups {
		if binding.owner == scope {
			continue
		}
		out = append(out, binding)
	}
	s.scopedCleanups = out
}
func (s *functionState) emitScopeCleanups(scope *codegenScope) error {
	if scope == nil {
		return nil
	}
	for pass := 0; pass < 2; pass++ {
		for i := len(s.scopedCleanups) - 1; i >= 0; i-- {
			if s.currentBlockTerminated() {
				break
			}
			binding := s.scopedCleanups[i]
			if binding.owner != scope {
				continue
			}
			isJoin := binding.kind == scopedCleanupTaskGroupWait
			if isJoin != (pass == 0) {
				continue
			}
			if err := s.emitScopedCleanup(binding); err != nil {
				return err
			}
		}
	}
	s.discardScopeCleanups(scope)
	return nil
}
func (s *functionState) emitBlockInCurrentScope(stmts []ast.Stmt) error {
	scope := s.scope
	// Like the scoped emitBlock path, snapshot the packed-store bindings: this body (a match arm,
	// loop body, or deferred body) lands in basic blocks that do not dominate its siblings or
	// successors, so an implicit region-backed store built here (docs/74 getOrCreateRegionPackedStore)
	// must not leak out — reusing its SSA value elsewhere fails the LLVM dominance verifier.
	savedPackedStores := s.packedStores
	s.packedStores = s.clonePackedStores()
	defer func() {
		s.packedStores = savedPackedStores
	}()
	if err := s.emitBlock(stmts, false); err != nil {
		s.discardScopeCleanups(scope)
		return err
	}
	if s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return nil
	}
	return s.emitScopeCleanups(scope)
}
func backendScopedArenaOwnerRefType(pos lexer.Pos) ast.TypeExpr {
	return &ast.MutableType{Position: pos, Elem: &ast.RefType{
		Position: pos,
		Elem:     &ast.NamedType{Position: pos, Name: "Arena"},
		State:    ast.RefStateNonNull,
		Storage:  ast.RefStorageAny,
		Explicit: true,
	}}
}
func backendScopedArenaOwnerDecl(pos lexer.Pos, regionName string, ownerName string) *ast.VarDeclStmt {
	ownerType := backendScopedArenaOwnerRefType(pos)
	return &ast.VarDeclStmt{
		Position: pos,
		Name:     ownerName,
		Type:     ownerType,
		Value: &ast.CastExpr{
			Position: pos,
			Operand:  &ast.AddrOfExpr{Position: pos, Operand: &ast.Ident{Position: pos, Name: regionName}},
			Target:   ownerType,
		},
	}
}

// noteScopedArenaOwnerAlias records that `as OWNER` names the same arena as the region
// itself, and returns a restore for the enclosing binding of that name. Without it, `in
// owner:` keys the tree-alloc owner on the alloca holding the REFERENCE while `in scratch:`
// keys on the Arena alloca, so one arena had two identities and a packed store registered
// under one was invisible under the other.
func (s *functionState) noteScopedArenaOwnerAlias(stmt *ast.RegionStmt) func() {
	if s == nil || stmt == nil || stmt.OwnerName == "" {
		return func() {}
	}
	if s.scopedArenaOwnerAlias == nil {
		s.scopedArenaOwnerAlias = make(map[string]string)
	}
	previous, had := s.scopedArenaOwnerAlias[stmt.OwnerName]
	s.scopedArenaOwnerAlias[stmt.OwnerName] = stmt.Name
	return func() {
		if had {
			s.scopedArenaOwnerAlias[stmt.OwnerName] = previous
			return
		}
		delete(s.scopedArenaOwnerAlias, stmt.OwnerName)
	}
}

func backendScopedArenaInStoreStmt(stmt *ast.RegionStmt) *ast.InStoreStmt {
	body := make([]ast.Stmt, 0, len(stmt.Body)+1)
	if stmt.OwnerName != "" {
		body = append(body, backendScopedArenaOwnerDecl(stmt.Position, stmt.Name, stmt.OwnerName))
	}
	body = append(body, stmt.Body...)
	return &ast.InStoreStmt{
		Position: stmt.Position,
		Store:    &ast.Ident{Position: stmt.Position, Name: stmt.Name},
		Body:     body,
	}
}
func (s *functionState) emitRegionDecl(n *ast.RegionStmt) error {
	return s.emitRegionDeclImpl(n, false)
}

// emitRegionDeclImpl lowers a region declaration. When loopReset is true (a lazy region entered in
// a loop), the arena slot is zeroed ONCE in the entry block instead of on every iteration — so the
// region's blocks, reset (not freed) at each scope exit, are reused across iterations.
func (s *functionState) emitRegionDeclImpl(n *ast.RegionStmt, loopReset bool) error {
	arenaType := s.g.namedType("Arena")
	if arenaType == nil {
		return fmt.Errorf("missing builtin Arena type for region %s", n.Name)
	}
	var alloca llvmValueRef
	var err error
	if loopReset {
		// Zero once in the entry block; do NOT re-zero per iteration (that would lose the blocks).
		alloca, err = s.createEntryAllocaZeroed(n.Name, arenaType)
		if err != nil {
			return err
		}
	} else {
		// The declaration may itself be nested in a conditional or loop.  Its cleanup is
		// emitted at the scope's CFG merge, which can also be reached when the declaration
		// was not entered.  Initialize the entry-dominating arena slot on every path so that
		// that merge cleanup sees a valid zero arena instead of uninitialized stack bytes.
		// Non-lazy regions are initialized with their first block below on the path that
		// enters the declaration; this entry store is only the fail-safe empty state.
		alloca, err = s.createEntryAllocaZeroed(n.Name, arenaType)
		if err != nil {
			return err
		}
	}
	s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: arenaType})
	s.regions = append(s.regions, regionBinding{name: n.Name, ptr: alloca, typ: arenaType, owned: true})
	s.treeAllocOwner = treeAllocOwnerBinding{arenaRef: alloca}
	// A lazy region (e.g. `in auto:`) is left zero-initialized: arena_alloc creates its
	// first block on demand, and arena_free over a never-allocated arena is a no-op. So a
	// region that never allocates costs nothing beyond the stack slot.
	if n.Lazy {
		s.treeAllocOwner.storeAnchorBlock, s.treeAllocOwner.storeAnchorInstr = s.captureStoreAnchor()
		return nil
	}
	if err := s.emitRegionInit(alloca, arenaType, n.Capacity, n.Allocator); err != nil {
		return err
	}
	// Anchor implicit packed-store creation right after the arena is fully initialized:
	// getOrCreateRegionPackedStore hoists ctx_aos_store_new here so one store per (region,
	// enum) dominates every use, instead of re-creating a store at a first use inside a loop.
	s.treeAllocOwner.storeAnchorBlock, s.treeAllocOwner.storeAnchorInstr = s.captureStoreAnchor()
	return nil
}

// regionPolyAutoAdopts reports whether this synthesized `__auto_*` region, inside a
// region-polymorphic function (docs/75), should ADOPT the threaded caller region (`__region_auto`)
// instead of creating its own arena. When true the region allocates nothing, frees nothing, and is
// NOT added to s.regions (the caller owns it) — `new[auto]` in its body lands in the caller's arena.
func (s *functionState) regionPolyAutoAdopts(n *ast.RegionStmt) bool {
	if s == nil {
		return false
	}
	return backendRegionPolyAutoAdopts(s.fnType, s.regionPolyOwner.arenaRef != nil, s.ambientGrownContainerRegion, n)
}

func backendRegionPolyAutoAdopts(fnType *semantic.FuncType, hasArenaOwner bool, ambientGrownContainerRegion string, n *ast.RegionStmt) bool {
	if fnType == nil || !backendIsCompilerGeneratedAutoRegion(n) || !hasArenaOwner {
		return false
	}
	// A region-polymorphic function adopts the threaded caller region. So does a VOID GROWER whose
	// ambient region was bound to a grown caller-owned container's arena (AmbientGrownContainerRegion):
	// its synthesized `__auto_*` region must route inserted region-poly values into that arena, not a
	// per-call arena freed on return.
	return fnType.RegionPolymorphic || ambientGrownContainerRegion != ""
}

func backendSynthesizedAutoRegionActive(parentActive bool, n *ast.RegionStmt) bool {
	return parentActive || backendIsCompilerGeneratedAutoRegion(n)
}

func backendIsSynthesizedAutoRegion(name string) bool {
	const prefix = "__auto_"
	return len(name) > len(prefix) && name[:len(prefix)] == prefix
}

func backendIsCompilerGeneratedAutoRegion(n *ast.RegionStmt) bool {
	return n != nil && n.Lazy && !n.UserAuto && backendIsSynthesizedAutoRegion(n.Name)
}

// backendTypeCarriesRegionStorage mirrors the semantic lifetime predicate for the backend's one
// allocation-routing decision. A scalar result must not redirect a region-polymorphic call made
// for side effects, while a container/borrow/aggregate result may retain the callee's allocation.
func backendTypeCarriesRegionStorage(t semantic.Type) bool {
	return backendTypeCarriesRegionStorageRec(t, map[semantic.Type]bool{})
}

func backendTypeCarriesRegionStorageRec(t semantic.Type, seen map[semantic.Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *semantic.DArrayType, *semantic.DictType, *semantic.SetType, *semantic.CStrType, *semantic.SViewType, *semantic.ViewType:
		return true
	case *semantic.RefType:
		return tt != nil && backendTypeCarriesRegionStorageRec(tt.Elem, seen)
	case *semantic.OptionalType:
		return tt != nil && backendTypeCarriesRegionStorageRec(tt.Value, seen)
	case *semantic.TupleType:
		if tt == nil {
			return false
		}
		for _, field := range tt.Fields {
			if backendTypeCarriesRegionStorageRec(field.Type, seen) {
				return true
			}
		}
	case *semantic.AggregateStateType:
		return tt != nil && backendTypeCarriesRegionStorageRec(tt.Base, seen)
	case *semantic.StructType:
		if tt == nil {
			return false
		}
		if tt.Store {
			return true
		}
		for _, field := range tt.Fields {
			if backendTypeCarriesRegionStorageRec(field.Type, seen) {
				return true
			}
		}
	case *semantic.GenericInstanceType:
		if tt == nil {
			return false
		}
		if backendTypeCarriesRegionStorageRec(tt.Base, seen) {
			return true
		}
		for _, arg := range tt.Args {
			if backendTypeCarriesRegionStorageRec(arg, seen) {
				return true
			}
		}
	case *semantic.EnumType:
		if tt == nil || tt.Packed || tt.Root().Packed {
			return false
		}
		for _, variant := range tt.Variants {
			if variant == nil {
				continue
			}
			for _, payload := range variant.Payload {
				if backendTypeCarriesRegionStorageRec(payload, seen) {
					return true
				}
			}
		}
	}
	return false
}

func backendMutatesAggregateMethod(name string) bool {
	switch name {
	case "push", "extend", "insert", "put", "add", "append", "set", "update":
		return true
	default:
		return false
	}
}

func backendAggregateRootName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		if n != nil {
			return n.Name
		}
	case *ast.FieldExpr:
		if n != nil {
			return backendAggregateRootName(n.Object)
		}
	case *ast.IndexExpr:
		if n != nil {
			return backendAggregateRootName(n.Object)
		}
	case *ast.SliceExpr:
		if n != nil {
			return backendAggregateRootName(n.Object)
		}
	case *ast.ParenExpr:
		if n != nil {
			return backendAggregateRootName(n.Inner)
		}
	case *ast.AddrOfExpr:
		if n != nil {
			return backendAggregateRootName(n.Operand)
		}
	}
	return ""
}

// backendAggregateIdentifierNames returns source identifiers contained in an ordinary value
// expression. It is intentionally called only on mutator arguments, never on the callee, so a
// receiver such as `terminators` is not mistaken for a value copied into itself. The scan is
// conservative: retaining one extra local in the caller arena is safe, while missing a nested
// storage dependency leaves a dangling darray backing after the callee's auto region is freed.
func backendAggregateIdentifierNames(expr ast.Expr) map[string]bool {
	names := map[string]bool{}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		if v.Kind() == reflect.Interface {
			if !v.IsNil() {
				walk(v.Elem())
			}
			return
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			if ident, ok := v.Interface().(*ast.Ident); ok && ident != nil {
				names[ident.Name] = true
				return
			}
			walk(v.Elem())
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for _, i := range astWalkFields(v.Type()) {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			if !astTypeMayHoldNodes(v.Type().Elem()) {
				break
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(expr))
	return names
}

// callerStorageArenasForBody computes the direct ownership edges created by mutating calls into
// caller-owned aggregate parameters. For example, `terminators.push(term)` records that `term`
// must be allocated in terminators' region. The fixed point also handles `outer.push(inner)` after
// `inner.push(value)`, and avoids a broad all-results-to-caller-arena rule that would make unrelated
// temporary allocations violate reserve/fixed tail growth.
