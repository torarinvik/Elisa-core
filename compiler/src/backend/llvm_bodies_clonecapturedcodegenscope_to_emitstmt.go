//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

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
	info := s.g.result.Defer[stmt]
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
func (s *functionState) markRegionScopeOwned(ptr C.LLVMValueRef) {
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
	for i := len(s.scopedCleanups) - 1; i >= 0; i-- {
		if s.currentBlockTerminated() {
			break
		}
		binding := s.scopedCleanups[i]
		if binding.owner != scope {
			continue
		}
		if err := s.emitScopedCleanup(binding); err != nil {
			return err
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
	arenaType := s.g.result.NamedTypes["Arena"]
	if arenaType == nil {
		return fmt.Errorf("missing builtin Arena type for region %s", n.Name)
	}
	var alloca C.LLVMValueRef
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
	if s == nil || s.fnType == nil || n == nil || !n.Lazy || !backendIsSynthesizedAutoRegion(n.Name) || s.regionPolyOwner.arenaRef == nil {
		return false
	}
	// A region-polymorphic function adopts the threaded caller region. So does a VOID GROWER whose
	// ambient region was bound to a grown caller-owned container's arena (AmbientGrownContainerRegion):
	// its synthesized `__auto_*` region must route inserted region-poly values into that arena, not a
	// per-call arena freed on return.
	return s.fnType.RegionPolymorphic || s.ambientGrownContainerRegion != ""
}

func backendIsSynthesizedAutoRegion(name string) bool {
	const prefix = "__auto_"
	return len(name) > len(prefix) && name[:len(prefix)] == prefix
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
	case *semantic.DArrayType, *semantic.DictType, *semantic.SetType, *semantic.DStrType, *semantic.SViewType, *semantic.ViewType:
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
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
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
func (s *functionState) callerStorageArenasForBody(decl *ast.FuncDecl, fnType *semantic.FuncType, body []ast.Stmt) map[string]C.LLVMValueRef {
	result := map[string]C.LLVMValueRef{}
	if s == nil || decl == nil || fnType == nil {
		return result
	}
	paramArenas := map[string]C.LLVMValueRef{}
	explicitCount := backendExplicitParamCount(fnType, decl)
	if explicitCount > len(decl.Params) {
		explicitCount = len(decl.Params)
	}
	for i := 0; i < explicitCount; i++ {
		region := containerRegionName(fnType.Params[i])
		if region == "" {
			continue
		}
		if owner, ok := s.regionArenaOwner(region); ok && owner.arenaRef != nil {
			paramArenas[decl.Params[i].Name] = owner.arenaRef
		}
	}
	dependencies := map[string]map[string]bool{}
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
			if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
				if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && backendMutatesAggregateMethod(field.Field) {
					dst := backendAggregateRootName(field.Object)
					if dst != "" {
						deps := dependencies[dst]
						if deps == nil {
							deps = map[string]bool{}
							dependencies[dst] = deps
						}
						for _, arg := range call.Args {
							for name := range backendAggregateIdentifierNames(arg) {
								deps[name] = true
							}
						}
					}
				}
			}
			walk(v.Elem())
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(body))
	for name, arena := range paramArenas {
		result[name] = arena
	}
	for changed := true; changed; {
		changed = false
		for owner, sources := range dependencies {
			arena, escapes := result[owner]
			if !escapes || arena == nil {
				continue
			}
			for source := range sources {
				if _, already := result[source]; already {
					continue
				}
				result[source] = arena
				changed = true
			}
		}
	}
	return result
}

// adoptedEscapeNamesForBody computes the ownership fact needed when a synthesized auto region
// adopts a caller arena. A local returned from the function, copied as an ordinary call argument,
// or used to initialize another value that may escape must remain in the caller arena. Method
// receivers are deliberately excluded: `report.push(value)` mutates report in place, but its
// argument is the only value that can be copied into report. The scan is conservative; a false
// positive retains caller ownership, while a false negative would leave a dangling container
// header after the temporary arena is freed.
func adoptedEscapeNamesForBody(body []ast.Stmt) map[string]bool {
	escaped := map[string]bool{}
	// dependencies records values copied into a mutable aggregate.  A returned
	// aggregate is an escape root, so every value copied into it must use the
	// caller-owned region too.  This is especially important for a struct pushed
	// into a returned darray: a shallow aggregate copy otherwise leaves a nested
	// darray backing in adopted.scratch, which is freed before the return value is
	// observed.
	dependencies := map[string]map[string]bool{}
	addDependencies := func(dst string, values []ast.Expr) {
		if dst == "" {
			return
		}
		deps := dependencies[dst]
		if deps == nil {
			deps = map[string]bool{}
			dependencies[dst] = deps
		}
		var collectNames func(reflect.Value)
		collectNames = func(v reflect.Value) {
			if !v.IsValid() || !v.CanInterface() {
				return
			}
			if v.Kind() == reflect.Interface {
				if !v.IsNil() {
					collectNames(v.Elem())
				}
				return
			}
			if v.Kind() == reflect.Pointer {
				if v.IsNil() {
					return
				}
				if ident, ok := v.Interface().(*ast.Ident); ok && ident != nil {
					deps[ident.Name] = true
					return
				}
				collectNames(v.Elem())
				return
			}
			switch v.Kind() {
			case reflect.Struct:
				for i := 0; i < v.NumField(); i++ {
					collectNames(v.Field(i))
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					collectNames(v.Index(i))
				}
			}
		}
		for _, value := range values {
			collectNames(reflect.ValueOf(value))
		}
	}
	var aggregateRootName func(ast.Expr) string
	aggregateRootName = func(expr ast.Expr) string {
		switch n := expr.(type) {
		case *ast.Ident:
			if n != nil {
				return n.Name
			}
		case *ast.FieldExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.IndexExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.SliceExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.ParenExpr:
			if n != nil {
				return aggregateRootName(n.Inner)
			}
		case *ast.AddrOfExpr:
			if n != nil {
				return aggregateRootName(n.Operand)
			}
		}
		return ""
	}
	// These are the built-in mutators that copy their ordinary argument values
	// into a receiver.  Read-only methods are intentionally excluded so a value
	// returned from a function does not unnecessarily retain unrelated scratch
	// allocations.
	mutatesAggregate := func(name string) bool {
		switch name {
		case "push", "extend", "insert", "put", "add", "append", "set", "update":
			return true
		default:
			return false
		}
	}
	var collectExpr func(reflect.Value)
	collectExpr = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return
			}
			collectExpr(v.Elem())
			return
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			if ident, ok := v.Interface().(*ast.Ident); ok && ident != nil {
				escaped[ident.Name] = true
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
				// A field/scope callee is a receiver, not a copied value. Its
				// ordinary arguments are still escape candidates.
				for _, arg := range call.Args {
					collectExpr(reflect.ValueOf(arg))
				}
				return
			}
			collectExpr(v.Elem())
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				collectExpr(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				collectExpr(v.Index(i))
			}
		}
	}
	var walkStmt func(reflect.Value)
	walkStmt = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return
			}
			walkStmt(v.Elem())
			return
		}
		if v.Kind() != reflect.Pointer {
			switch v.Kind() {
			case reflect.Struct:
				for i := 0; i < v.NumField(); i++ {
					walkStmt(v.Field(i))
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					walkStmt(v.Index(i))
				}
			}
			return
		}
		if v.IsNil() {
			return
		}
		if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
			if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && mutatesAggregate(field.Field) {
				addDependencies(aggregateRootName(field.Object), call.Args)
			}
		}
		if ret, ok := v.Interface().(*ast.ReturnStmt); ok && ret != nil {
			collectExpr(reflect.ValueOf(ret.Value))
		}
		if decl, ok := v.Interface().(*ast.VarDeclStmt); ok && decl != nil {
			// Propagate ownership through `local = other`; if the local is later
			// returned/copied, keeping the source in scratch would be unsound.
			collectExpr(reflect.ValueOf(decl.Value))
		}
		if assign, ok := v.Interface().(*ast.AssignStmt); ok && assign != nil {
			collectExpr(reflect.ValueOf(assign.Value))
		}
		walkStmt(v.Elem())
	}
	walkStmt(reflect.ValueOf(body))
	// Propagate escape through aggregate-copy edges until reaching a fixed point.
	// This handles chains such as `inner` -> `arms_buffer` -> return, while still
	// keeping unrelated scratch containers private.
	for changed := true; changed; {
		changed = false
		for owner, sources := range dependencies {
			if !escaped[owner] {
				continue
			}
			for source := range sources {
				if !escaped[source] {
					escaped[source] = true
					changed = true
				}
			}
		}
	}
	return escaped
}

func (s *functionState) ensureAdoptedScratchArena() error {
	if s == nil || s.scratchArena != nil {
		return nil
	}
	if s.g == nil || s.g.result == nil || s.g.result.NamedTypes["Arena"] == nil {
		return fmt.Errorf("missing builtin Arena type for adopted scratch arena")
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	alloca, err := s.createEntryAllocaZeroed("adopted.scratch", arenaType)
	if err != nil {
		return err
	}
	s.scratchArena = alloca
	s.registerFunctionCleanup(scopedCleanupBinding{kind: scopedCleanupRegion, name: "adopted.scratch", ptr: alloca, typ: arenaType})
	return nil
}

func (s *functionState) adoptedScratchForLocal(name string) C.LLVMValueRef {
	if s == nil || s.scratchArena == nil {
		return nil
	}
	if s.adoptedEscapeNames != nil && s.adoptedEscapeNames[name] {
		return nil
	}
	return s.scratchArena
}

func (s *functionState) emitScopedArenaStmt(n *ast.RegionStmt) error {
	s.pushScope()
	scope := s.scope
	defer s.popScope()
	savedTreeOwner := s.treeAllocOwner
	savedSynthesizedAutoRegion := s.activeSynthesizedAutoRegion
	if backendIsSynthesizedAutoRegion(n.Name) {
		s.activeSynthesizedAutoRegion = true
	}
	// docs/74: the implicit region-backed packed stores are region-scoped — a store built in this
	// region must not leak out to (or into) an enclosing region. Clone on entry, restore on exit, so
	// each `in auto:` owns its own stores and a long-lived outer tree keeps its store across inner
	// per-iteration regions.
	savedPackedStores := s.packedStores
	s.packedStores = s.clonePackedStores()
	restoreOwnerAlias := s.noteScopedArenaOwnerAlias(n)
	defer func() {
		s.treeAllocOwner = savedTreeOwner
		s.activeSynthesizedAutoRegion = savedSynthesizedAutoRegion
		s.packedStores = savedPackedStores
		restoreOwnerAlias()
	}()
	if s.regionPolyAutoAdopts(n) {
		// Adopt the threaded caller region: bind the region name to it, route new[auto] to it, emit
		// the body, and run scope cleanups. The result-bearing stack stays in the caller's arena, but
		// non-escaping growable locals get private stacks below. This distinction matters for a
		// region-polymorphic builder such as `read_file`: its returned `contents` must be adopted by
		// the caller, while its scratch `chunk` must not be allocated on top of `contents` in a
		// reserve_commit arena. The old all-or-nothing adoption made that second growth panic at
		// runtime with "not the tail allocation".
		s.defineBinding(n.Name, valueBinding{ptr: s.regionPolyOwner.arenaRef, typ: s.g.result.NamedTypes["Arena"]})
		s.treeAllocOwner = s.regionPolyOwner
		if err := s.ensureAdoptedScratchArena(); err != nil {
			return err
		}
		if assignment, ok := s.g.result.RegionStacks[n]; ok && assignment.StackCount > 1 {
			escaped := adoptedRegionEscapingStacks(n, assignment)
			private := adoptedRegionStackAssignment(assignment, escaped)
			restoreTags, err := s.emitRegionExtraStacksWithAssignment(n, false, private)
			if err != nil {
				return err
			}
			defer restoreTags()
		}
		if err := s.emitInStore(backendScopedArenaInStoreStmt(n)); err != nil {
			return err
		}
		return s.emitScopeCleanups(scope)
	}
	// Reclaim the region at SCOPE exit rather than deferring to function return. The escape
	// checker guarantees nothing escapes a scoped region, so this is sound; registering it as a
	// scoped cleanup makes it fire on every exit path (normal, break, continue, return), and the
	// idempotent arena_free + s.regions function-return safety net make any overlap a no-op.
	//
	// For a LAZY region entered inside a loop, reset-and-reuse: zero the arena once (in the entry
	// block) and arena_reset (keep blocks) at each scope exit, so iterations 2..N reuse the blocks
	// with zero mmap/munmap syscalls; the function-return arena_free releases them. Otherwise free
	// at scope exit (releases memory promptly for non-loop scopes).
	loopReset := n.Lazy && len(s.continueTargets) > 0
	if err := s.emitRegionDeclImpl(n, loopReset); err != nil {
		return err
	}
	if binding, ok := s.lookupBinding(n.Name); ok && binding.ptr != nil {
		kind := scopedCleanupRegion
		if loopReset {
			kind = scopedCleanupRegionReset
		}
		s.registerScopedCleanup(scopedCleanupBinding{kind: kind, name: n.Name, ptr: binding.ptr, typ: binding.typ})
		if !loopReset {
			s.markRegionScopeOwned(binding.ptr)
		}
	}
	restoreTags, err := s.emitRegionExtraStacks(n, loopReset)
	if err != nil {
		return err
	}
	defer restoreTags()
	if err := s.emitInStore(backendScopedArenaInStoreStmt(n)); err != nil {
		return err
	}
	return s.emitScopeCleanups(scope)
}

// emitRegionExtraStacks lowers the parallel arenas of a multi-stack region (Phase B1b, docs/71):
// stack 0 is the region's main arena (already emitted); each growable/merge stack 1..N-1 gets its
// own lazy arena, freed with the region. It returns a closure that restores the darray->arena tag
// routing map to its prior state on region exit (handling nested same-name regions).
// emitReserveCommitStackInit eagerly creates a reserve_commit arena whose contiguous reservation is
// sized to hold the darray's proven maximum footprint. The bound N is an element count; the buffer
// after arena_da_reserve's power-of-two growth is at most nextPow2(N) <= 2N elements, so the
// reservation is sized to 2*N*sizeof(elem) bytes plus headroom (in uintptr slots). reserve_commit
// commits pages lazily, so over-reserving costs only virtual address space.
func (s *functionState) emitReserveCommitStackInit(arenaPtr C.LLVMValueRef, arenaType semantic.Type, boundExpr ast.Expr, elemType semantic.Type) error {
	if boundExpr == nil || elemType == nil {
		return fmt.Errorf("reserve_commit stack missing bound or element type")
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVM, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	nValue, _, err := s.emitExpr(boundExpr, usizeType)
	if err != nil {
		return err
	}
	elemBytes, err := s.sizeOfType(elemType)
	if err != nil {
		return err
	}
	if elemBytes == 0 {
		elemBytes = 1
	}
	twoN, err := s.emitCheckedUSizeMul(nValue, C.LLVMConstInt(usizeLLVM, 2, 0), "rc.2n")
	if err != nil {
		return err
	}
	bytes, err := s.emitCheckedUSizeMul(twoN, C.LLVMConstInt(usizeLLVM, C.ulonglong(elemBytes), 0), "rc.bytes")
	if err != nil {
		return err
	}
	bytes, err = s.emitCheckedUSizeAdd(bytes, C.LLVMConstInt(usizeLLVM, 4096, 0), "rc.bytes.headroom")
	if err != nil {
		return err
	}
	slots := C.LLVMBuildUDiv(s.builder, bytes, C.LLVMConstInt(usizeLLVM, 8, 0), cStringFree("rc.slots"))
	slots, err = s.emitCheckedUSizeAdd(slots, C.LLVMConstInt(usizeLLVM, 8, 0), "rc.slots.pad")
	if err != nil {
		return err
	}
	return s.emitRegionInitValue(arenaPtr, arenaType, slots, 2 /* ARENA_STRATEGY_RESERVE_COMMIT */)
}

// emitDefaultReserveCommitStackInit marks a default-backed growable tail stack as reserve_commit
// WITHOUT reserving here (docs/73 §3, lazy). It only sets Arena.strategy (field 3); the runtime
// arena_alloc reserves the default contiguous range on first allocation (ARENA_DEFAULT_RESERVE_
// COMMIT_SLOTS), so an empty or never-grown region costs nothing — not even an mmap. The base is
// still stable (one contiguous reservation, grows in place) and overflow still panics.
func (s *functionState) emitDefaultReserveCommitStackInit(arenaPtr C.LLVMValueRef, arenaType semantic.Type) error {
	arenaLLVMType, err := s.g.lowerType(arenaType)
	if err != nil {
		return err
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVM, err := s.g.lowerType(intType)
	if err != nil {
		return err
	}
	strategyPtr := C.LLVMBuildStructGEP2(s.builder, arenaLLVMType, arenaPtr, 3, cStringFree("region.strategy.lazy"))
	C.LLVMBuildStore(s.builder, C.LLVMConstInt(intLLVM, 2 /* ARENA_STRATEGY_RESERVE_COMMIT */, 0), strategyPtr)
	return nil
}

func (s *functionState) emitRegionExtraStacks(n *ast.RegionStmt, loopReset bool) (func(), error) {
	noop := func() {}
	asn, ok := s.g.result.RegionStacks[n]
	if !ok || asn.StackCount <= 1 {
		return noop, nil
	}
	return s.emitRegionExtraStacksWithAssignment(n, loopReset, asn)
}

// emitRegionExtraStacksWithAssignment is the common implementation for ordinary inferred regions
// and adopted region-polymorphic regions. In the ordinary case every stack is private to the
// current function/scope. In the adopted case the caller-owned result stacks are rewritten to stack
// zero by adoptedRegionStackAssignment, while only non-escaping stacks get a local arena.
func (s *functionState) emitRegionExtraStacksWithAssignment(n *ast.RegionStmt, loopReset bool, asn semantic.RegionStackAssignment) (func(), error) {
	noop := func() {}
	if asn.StackCount <= 1 {
		return noop, nil
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	if arenaType == nil {
		return noop, fmt.Errorf("missing builtin Arena type for region %s", n.Name)
	}
	allocaByStack := map[int]C.LLVMValueRef{}
	for k := 1; k < asn.StackCount; k++ {
		name := fmt.Sprintf("%s#%d", n.Name, k)
		var alloca C.LLVMValueRef
		var err error
		if loopReset {
			alloca, err = s.createEntryAllocaZeroed(name, arenaType)
			if err != nil {
				return noop, err
			}
		} else {
			// As with the primary region slot, this stack can be cleaned up at a
			// merge that is reachable without entering the scoped region.  Give it
			// an entry-dominating empty state before any path-specific initialization.
			alloca, err = s.createEntryAllocaZeroed(name, arenaType)
			if err != nil {
				return noop, err
			}
		}
		// Lazy arena: first block allocated on demand, free is a no-op if unused -> an unused
		// parallel stack costs only the stack slot.
		allocaByStack[k] = alloca
		s.regions = append(s.regions, regionBinding{name: name, ptr: alloca, typ: arenaType, owned: true})
		kind := scopedCleanupRegion
		if loopReset {
			kind = scopedCleanupRegionReset
		}
		s.registerScopedCleanup(scopedCleanupBinding{kind: kind, name: name, ptr: alloca, typ: arenaType})
		if !loopReset {
			s.markRegionScopeOwned(alloca)
		}
		// Phase C1c: a reserve_commit stack is eagerly initialized with a contiguous reservation
		// sized to its proven element bound, so the base never moves and interior refs survive
		// growth. Skipped under loopReset (a per-iteration reset region) — chained stays correct
		// there. The strategy is only assigned when the footprint is provably <= N (docs/72), so
		// the reservation can never overflow.
		if asn.StackStrategy[k] == "reserve_commit" {
			var initErr error
			if asn.StackCapacity[k] != nil && !loopReset {
				// Bounded (Phase C), non-loop: eager reservation sized to the proven footprint.
				initErr = s.emitReserveCommitStackInit(alloca, arenaType, asn.StackCapacity[k], asn.StackElemType[k])
			} else {
				// Default (docs/73) — and ALL loop-body reserve_commit: lazy. Set the strategy only;
				// the runtime reserves the default range on first alloc and arena_reset keeps it
				// across iterations (reserve once, reset per iteration — no per-iteration mmap). A
				// loop-body bound is intentionally ignored here: an eager loop-variable-dependent
				// reservation can't be hoisted, and the lazy default is sound (stable base) anyway.
				initErr = s.emitDefaultReserveCommitStackInit(alloca, arenaType)
			}
			if initErr != nil {
				return noop, initErr
			}
		}
	}
	if s.darrayStackTag == nil {
		s.darrayStackTag = map[string]string{}
	}
	type prevTag struct {
		value string
		had   bool
	}
	saved := map[string]prevTag{}
	for allocName, stackID := range asn.StackOf {
		if stackID <= 0 {
			continue // stack 0 = the main arena (ambient); no tag needed
		}
		v, had := s.darrayStackTag[allocName]
		saved[allocName] = prevTag{value: v, had: had}
		s.darrayStackTag[allocName] = fmt.Sprintf("%s#%d", n.Name, stackID)
	}
	// Phase B2: schedule each early-freeable own stack's arena to be freed right after the
	// statement at its recorded offset.
	if s.earlyFreeByOffset == nil {
		s.earlyFreeByOffset = map[int]C.LLVMValueRef{}
	}
	type prevFree struct {
		value C.LLVMValueRef
		had   bool
	}
	savedFree := map[int]prevFree{}
	for k, off := range asn.StackEarlyFreeAfter {
		if alloca, ok := allocaByStack[k]; ok {
			v, had := s.earlyFreeByOffset[off]
			savedFree[off] = prevFree{value: v, had: had}
			s.earlyFreeByOffset[off] = alloca
		}
	}
	return func() {
		for name, p := range saved {
			if p.had {
				s.darrayStackTag[name] = p.value
			} else {
				delete(s.darrayStackTag, name)
			}
		}
		for off, p := range savedFree {
			if p.had {
				s.earlyFreeByOffset[off] = p.value
			} else {
				delete(s.earlyFreeByOffset, off)
			}
		}
	}, nil
}

// withAdoptedEscapes keeps escaping allocations on the caller-owned ambient stack (stack zero) and
// leaves every non-escaping stack private to the adopted function. Stack IDs are intentionally not
// renumbered: preserving the analyzer's IDs keeps the darray tags and the proven reserve/fixed
// strategy metadata aligned, while an unused private slot is lazy and costs no backing allocation.
func adoptedRegionStackAssignment(asn semantic.RegionStackAssignment, escaped map[int]bool) semantic.RegionStackAssignment {
	private := asn
	private.StackOf = make(map[string]int, len(asn.StackOf))
	for name, stack := range asn.StackOf {
		if escaped[stack] {
			private.StackOf[name] = 0
		} else {
			private.StackOf[name] = stack
		}
	}
	private.StackEarlyFreeAfter = make(map[int]int, len(asn.StackEarlyFreeAfter))
	for stack, offset := range asn.StackEarlyFreeAfter {
		for name, assigned := range private.StackOf {
			if assigned == stack && asn.StackOf[name] == stack {
				private.StackEarlyFreeAfter[stack] = offset
				break
			}
		}
	}
	return private
}

// adoptedRegionEscapingStacks conservatively identifies which inferred-region stacks contribute to
// a value returned by a region-polymorphic function. A direct returned local, a field/index rooted
// at that local, and a struct/tuple/call argument containing that local are all recognized through
// the AST walk. If the return shape cannot be tied to a known stack, all stacks remain caller-owned;
// that preserves the existing safety invariant instead of guessing that a value is temporary.
func adoptedRegionEscapingStacks(region *ast.RegionStmt, asn semantic.RegionStackAssignment) map[int]bool {
	escaped := map[int]bool{}
	if region == nil {
		return escaped
	}
	// Keep the stack-level escape computation aligned with adoptedEscapeNamesForBody. A returned
	// container can acquire region-backed values through a mutating method (for example
	// `arms.push(Arm{body: body})`), so the nested value's stack is an escape dependency even when it
	// is not itself named by the return expression.
	dependencies := map[string]map[string]bool{}
	addDependencies := func(dst string, values []ast.Expr) {
		if dst == "" {
			return
		}
		deps := dependencies[dst]
		if deps == nil {
			deps = map[string]bool{}
			dependencies[dst] = deps
		}
		var collectNames func(reflect.Value)
		collectNames = func(v reflect.Value) {
			if !v.IsValid() || !v.CanInterface() {
				return
			}
			if v.Kind() == reflect.Interface {
				if !v.IsNil() {
					collectNames(v.Elem())
				}
				return
			}
			if v.Kind() == reflect.Pointer {
				if v.IsNil() {
					return
				}
				if ident, ok := v.Interface().(*ast.Ident); ok && ident != nil {
					deps[ident.Name] = true
					return
				}
				collectNames(v.Elem())
				return
			}
			switch v.Kind() {
			case reflect.Struct:
				for i := 0; i < v.NumField(); i++ {
					collectNames(v.Field(i))
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					collectNames(v.Index(i))
				}
			}
		}
		for _, value := range values {
			collectNames(reflect.ValueOf(value))
		}
	}
	var aggregateRootName func(ast.Expr) string
	aggregateRootName = func(expr ast.Expr) string {
		switch n := expr.(type) {
		case *ast.Ident:
			if n != nil {
				return n.Name
			}
		case *ast.FieldExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.IndexExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.SliceExpr:
			if n != nil {
				return aggregateRootName(n.Object)
			}
		case *ast.ParenExpr:
			if n != nil {
				return aggregateRootName(n.Inner)
			}
		case *ast.AddrOfExpr:
			if n != nil {
				return aggregateRootName(n.Operand)
			}
		}
		return ""
	}
	mutatesAggregate := func(name string) bool {
		switch name {
		case "push", "extend", "insert", "put", "add", "append", "set", "update":
			return true
		default:
			return false
		}
	}
	sawReturn := false
	sawUnknown := false
	var walkExpr func(reflect.Value)
	walkExpr = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil {
				if stack, known := asn.StackOf[id.Name]; known {
					escaped[stack] = true
				} else {
					sawUnknown = true
				}
			}
			walkExpr(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walkExpr(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walkExpr(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walkExpr(v.Index(i))
			}
		}
	}
	var walkStmt func(reflect.Value)
	walkStmt = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
				if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && mutatesAggregate(field.Field) {
					addDependencies(aggregateRootName(field.Object), call.Args)
				}
			}
			if ret, ok := v.Interface().(*ast.ReturnStmt); ok && ret != nil {
				sawReturn = true
				walkExpr(reflect.ValueOf(ret.Value))
				return
			}
			walkStmt(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walkStmt(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walkStmt(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walkStmt(v.Index(i))
			}
		}
	}
	walkStmt(reflect.ValueOf(region.Body))
	for changed := true; changed; {
		changed = false
		for owner, sources := range dependencies {
			ownerStack, ownerKnown := asn.StackOf[owner]
			if !ownerKnown || !escaped[ownerStack] {
				continue
			}
			for source := range sources {
				if sourceStack, known := asn.StackOf[source]; known && !escaped[sourceStack] {
					escaped[sourceStack] = true
					changed = true
				}
			}
		}
	}
	if !sawReturn || sawUnknown {
		for _, stack := range asn.StackOf {
			escaped[stack] = true
		}
	}
	return escaped
}
func (s *functionState) emitDeferredBody(binding *deferredBodyBinding) error {
	if binding == nil || binding.stmt == nil {
		return nil
	}
	s.pushScope()
	defer s.popScope()
	s.injectCapturedScope(binding.captureScope)
	return s.emitBlockInCurrentScope(binding.stmt.Body)
}
func (s *functionState) emitBlock(stmts []ast.Stmt, scoped bool) error {
	// Invariants declared inside this block stop being re-checked once it exits (their variables may
	// leave scope). Truncate back to the count we entered with (docs/90 brick 90-14).
	savedInvariants := len(s.activeInvariants)
	defer func() { s.activeInvariants = s.activeInvariants[:savedInvariants] }()
	if scoped {
		savedPackedStores := s.packedStores
		s.packedStores = s.clonePackedStores()
		s.pushScope()
		scope := s.scope
		defer func() {
			s.popScope()
			s.packedStores = savedPackedStores
		}()
		for _, stmt := range stmts {
			if s.currentBlockTerminated() {
				break
			}
			if err := s.emitStmt(stmt); err != nil {
				return err
			}
		}
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil
		}
		return s.emitScopeCleanups(scope)
	}
	for _, stmt := range stmts {
		if s.currentBlockTerminated() {
			break
		}
		if err := s.emitStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitStmt(stmt ast.Stmt) error {
	if err := s.emitStmtInner(stmt); err != nil {
		return err
	}
	if err := s.recheckInvariantsAfter(stmt); err != nil {
		return err
	}
	return s.maybeEarlyFreeAfter(stmt)
}

// maybeEarlyFreeAfter frees an own-stack arena right after the top-level statement that ends its
// object's life (Phase B2). Idempotent with the region-exit cleanup (arena_free nulls begin/end);
// fired once per statement and skipped if the block already terminated.
func (s *functionState) maybeEarlyFreeAfter(stmt ast.Stmt) error {
	if s.earlyFreeByOffset == nil || stmt == nil || s.currentBlockTerminated() {
		return nil
	}
	arenaPtr, ok := s.earlyFreeByOffset[stmt.Pos().Offset]
	if !ok {
		return nil
	}
	delete(s.earlyFreeByOffset, stmt.Pos().Offset)
	return s.emitArenaFree(arenaPtr, s.g.result.NamedTypes["Arena"])
}

func (s *functionState) emitStmtInner(stmt ast.Stmt) error {
	if s.g.di != nil {
		s.g.di.setLoc(s, stmt)
	}
	if s.g.trace != nil {
		s.g.trace.recordStmt(s, stmt)
	}
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Ghost || (s.g != nil && s.g.result != nil && s.g.result.GhostDecls[n]) {
			// A `ghost` declaration is verification-only: the analyzer proved no real value depends on
			// it, so emit NOTHING (the variable, its initializer, and any side effects are erased).
			return nil
		}
		var declType semantic.Type
		var err error
		if n.Type != nil {
			declType, err = s.resolveTypeExpr(n.Type)
			if err != nil {
				return err
			}
		} else if n.Value != nil {
			declType = s.exprType(n.Value)
			if declType == nil {
				return fmt.Errorf("cannot infer type for variable %s", n.Name)
			}
		} else {
			return fmt.Errorf("variable %s requires a type or initializer", n.Name)
		}
		alloca, err := s.createEntryAlloca(n.Name, declType)
		if err != nil {
			return err
		}
		// The binding comes into scope AFTER its initializer is emitted, not before.
		//
		// A declaration that SHADOWS an outer local reads the outer one in its own
		// initializer — `i = i + 1` inside a loop body means "declare a new i, one more
		// than the enclosing i". Defining the binding first made `i` inside the
		// initializer resolve to the slot being declared, which nothing has stored to
		// yet: the emitted IR loads the fresh alloca, so the new local is
		// uninitialized-memory + 1 and the OUTER i never changes.
		//
		// Measured on `def main() -> i64: i: mutable i64 = 0; while i < 3: i = i + 1;
		// return i`: the INTERPRETER answers 3 and the native binary loops forever. Two
		// answers from one compiler, and the interpreter's is the correct one — a
		// declaration's initializer belongs to the enclosing scope.
		var initValue C.LLVMValueRef
		if n.Value != nil {
			savedInitializerCarriesRegionStorage := s.activeInitializerCarriesRegionStorage
			savedInitializerName := s.activeInitializerName
			s.activeInitializerCarriesRegionStorage = backendTypeCarriesRegionStorage(declType)
			s.activeInitializerName = n.Name
			defer func() {
				s.activeInitializerCarriesRegionStorage = savedInitializerCarriesRegionStorage
				s.activeInitializerName = savedInitializerName
			}()
			// A returned-container call in an adopted region-polymorphic function must
			// use the fresh local's owner, not the caller's ambient owner. Otherwise a
			// later growth of a caller-owned darray sees the temporary result after its
			// own backing allocation and reserve_commit correctly rejects the non-tail
			// realloc. Escaping locals retain the adopted caller owner.
			savedTreeOwner := s.treeAllocOwner
			if scratch := s.adoptedScratchForLocal(n.Name); scratch != nil {
				s.treeAllocOwner = treeAllocOwnerBinding{arenaRef: scratch}
				defer func() { s.treeAllocOwner = savedTreeOwner }()
			}
			// If this local is a stack-tagged darray, tell a seeded container initializer to
			// allocate its initial backing into that SAME parallel arena (consumed once by the
			// literal/comprehension emit), so later growth through a grower never straddles the
			// region base arena and the parallel arena (task_00a7fdf3).
			savedSink := s.currentDArraySinkTag
			if s.darrayStackTag != nil {
				if tag, ok := s.darrayStackTag[n.Name]; ok {
					s.currentDArraySinkTag = tag
				}
			}
			value, _, err := s.emitExpr(n.Value, declType)
			s.currentDArraySinkTag = savedSink
			if err != nil {
				return err
			}
			initValue = value
			// storeValue lowers large `zeroed` aggregates to llvm.memset and large
			// aggregate copies to memcpy, avoiding `store <bigtype> zeroinitializer`
			// (catastrophic for llc -O0 on multi-KB structs).
			if err := s.storeValue(alloca, value, declType, n.Name); err != nil {
				return err
			}
			s.bindPackedStoreValue(declType, value)
			if err := s.bindPackedStoreOriginsForExprPath(n.Name, n.Value, declType); err != nil {
				return err
			}
		}
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: declType, mutable: n.Mutable})
		// docs/126 D1: a drop-typed local arms its destructor here, after the binding is
		// in scope (the synthesized `__drop__(move n.Name)` resolves through it) and
		// after the initializer ran (a shadowing decl must not arm the outer value).
		if _, err := s.registerDropCleanup(n.Name, alloca, declType); err != nil {
			return err
		}
		if s.g.di != nil {
			s.g.di.declareVariable(s, n.Name, alloca, declType, n.Pos().Line, 0)
		}
		s.bindImplicitTreeOwnerParam(n.Name, declType, alloca, initValue)
		if s.g.trace != nil {
			s.g.trace.recordValue(s, n.Pos().Line, n.Name, initValue, declType)
		}
		if err := s.emitRefinementChecks(n); err != nil {
			return err
		}
		return nil
	case *ast.LetDestructureStmt:
		return s.emitLetDestructureStmt(n)
	case *ast.TupleBindStmt:
		if n.ArgManifest {
			// docs/120 §8: the names are a manifest of what a void call mutates in place —
			// emit only the call (its refs do the mutation), discard the (void) result.
			_, _, err := s.emitExpr(n.Value, nil)
			return err
		}
		return s.emitTupleBindStmt(n)
	case *ast.MoveBindStmt:
		return s.emitMoveBindStmt(n)
	case *ast.DeferStmt:
		return s.emitDeferStmt(n)
	case *ast.ScopeStmt:
		return s.emitScopeStmt(n)
	case *ast.RegionStmt:
		if len(n.Body) != 0 || n.OwnerName != "" {
			return s.emitScopedArenaStmt(n)
		}
		return s.emitRegionDecl(n)
	case *ast.MarkStmt:
		regionBinding, ok := s.lookupBinding(n.RegionName)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.RegionName)
		}
		markType := s.g.result.NamedTypes["ArenaMark"]
		if markType == nil {
			return fmt.Errorf("missing builtin ArenaMark type for region checkpoints")
		}
		alloca, err := s.createEntryAlloca(n.Name, markType)
		if err != nil {
			return err
		}
		markValue, err := s.emitArenaSnapshot(regionBinding.ptr, regionBinding.typ)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, markValue, alloca)
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: markType})
		return nil
	case *ast.CheckpointStmt:
		return s.emitCheckpointStmt(n)
	case *ast.GroupedCheckpointStmt:
		return s.emitGroupedCheckpointStmt(n)
	case *ast.RestoreStmt:
		regionBinding, ok := s.lookupBinding(n.RegionName)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.RegionName)
		}
		markBinding, ok := s.lookupBinding(n.MarkName)
		if !ok {
			return fmt.Errorf("unknown checkpoint %q during LLVM lowering", n.MarkName)
		}
		markValue, err := s.loadValue(markBinding.ptr, markBinding.typ, n.MarkName)
		if err != nil {
			return err
		}
		return s.emitArenaRewind(regionBinding.ptr, regionBinding.typ, markValue, markBinding.typ)
	case *ast.RestoreCheckpointStmt:
		return s.emitRestoreCheckpointStmt(n)
	case *ast.ResetStmt:
		regionBinding, ok := s.lookupBinding(n.Name)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Name)
		}
		return s.emitArenaReset(regionBinding.ptr, regionBinding.typ)
	case *ast.DestroyStmt:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Name)
		}
		return s.emitArenaFree(binding.ptr, binding.typ)
	case *ast.AdoptStmt:
		childBinding, ok := s.lookupBinding(n.Child)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Child)
		}
		parentBinding, ok := s.lookupBinding(n.Parent)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Parent)
		}
		return s.emitArenaAdopt(parentBinding.ptr, childBinding.ptr, parentBinding.typ)
	case *ast.PromoteStmt:
		return s.emitPromoteStmt(n)
	case *ast.AssignStmt:
		if n.ArgManifest {
			// docs/120 §8: `x <- x.method(…)` — the target manifests what a void call mutates
			// in place; emit only the call, discard the (void) result.
			_, _, err := s.emitExpr(n.Value, nil)
			return err
		}
		if n.AsOverlayCall != nil {
			// Guest-overlay write (docs/107): `base.field[mem] = value` was desugared by the analyzer
			// to a MemoryManager_WriteU<N> call. Emit that call in place of the store; the lowering is
			// byte-identical to the hand-written write.
			if s.g.trace != nil {
				s.g.trace.recordStep(s, n.Pos().Line)
			}
			_, _, err := s.emitCallExpr(n.AsOverlayCall)
			return err
		}
		if n.FastMath {
			// A comprehension fold's accumulator update: emit its value with reassociation+contraction
			// so the reduction re-brackets into the vectorizable tree form (the defined reduction order
			// for a fold, docs/79), scoped to this statement. The defer pops when emitStmt returns, i.e.
			// right after this assignment is emitted. Integer accumulators are unaffected by FP flags.
			s.reduceReassocScope++
			defer func() { s.reduceReassocScope-- }()
		}
		if n.Optional {
			if s.g.trace != nil {
				s.g.trace.recordStep(s, n.Pos().Line)
			}
			return s.emitOptionalAssignStmt(n)
		}
		if identTarget, ok := n.Target.(*ast.Ident); ok {
			if binding, ok := s.lookupBinding(identTarget.Name); ok && !binding.mutable {
				if refType, ok := binding.typ.(*semantic.RefType); ok {
					slotPtr, err := s.loadValue(binding.ptr, binding.typ, identTarget.Name+".ref.slot")
					if err != nil {
						return err
					}
					storeType := refType.Elem
					valueType := s.exprType(n.Value)
					if _, valueIsRef := valueType.(*semantic.RefType); semantic.SameType(valueType, binding.typ) || valueIsRef {
						storeType = binding.typ
					}
					if _, valueIsStringLiteral := n.Value.(*ast.StringLit); valueIsStringLiteral {
						storeType = binding.typ
					}
					value, _, err := s.emitExpr(n.Value, storeType)
					if err != nil {
						return err
					}
					if err := s.storeValue(slotPtr, value, storeType, identTarget.Name+".ref.assign"); err != nil {
						return err
					}
					if s.g.trace != nil {
						s.g.trace.recordValue(s, n.Pos().Line, identTarget.Name, value, storeType)
					}
					s.invalidatePackedReadCaches()
					return nil
				}
			}
		}
		if fieldTarget, ok := n.Target.(*ast.FieldExpr); ok {
			if handled, err := s.emitBitGroupMemberAssign(fieldTarget, n.Value); handled {
				if err != nil {
					return err
				}
				if s.g.trace != nil {
					s.g.trace.recordStep(s, n.Pos().Line)
				}
				s.invalidatePackedReadCaches()
				return nil
			}
		}
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		s.invalidatePackedEnumStoreOriginExpr(n.Target)
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedVariantViewExpr(n.Target)
		// Reassigning a seeded container into a stack-tagged darray (`xs <- [..]`) must allocate
		// the new backing into that darray's parallel arena, exactly like the VarDecl seed path —
		// otherwise a later grower's realloc straddles the region base arena (task_00a7fdf3).
		savedAssignSink := s.currentDArraySinkTag
		if id, ok := n.Target.(*ast.Ident); ok && s.darrayStackTag != nil {
			if tag, ok := s.darrayStackTag[id.Name]; ok {
				s.currentDArraySinkTag = tag
			}
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		s.currentDArraySinkTag = savedAssignSink
		if err != nil {
			return err
		}
		if err := s.storeValue(ptr, value, targetType, "assign"); err != nil {
			return err
		}
		// Struct field invariants (debug builds): after `obj.field <- v`, re-check obj's invariants.
		if s.g.optLevel == OptimizationLevel0 || s.g.forceContracts {
			if fieldTarget, ok := n.Target.(*ast.FieldExpr); ok && fieldTarget.Object != nil {
				if st := backendStructTypeOf(s.exprType(fieldTarget.Object)); st != nil && st.Decl != nil && len(st.Decl.Invariants) > 0 {
					objPtr, _, addrErr := s.emitAddress(fieldTarget.Object)
					if addrErr != nil {
						return addrErr
					}
					if err := s.emitStructInvariantChecksAt(objPtr, st); err != nil {
						return err
					}
				}
			}
		}
		if s.g.trace != nil {
			assignName := ""
			if id, ok := n.Target.(*ast.Ident); ok {
				assignName = id.Name
			}
			// Value record for a scalar ident; otherwise (field/index target, or a
			// non-scalar value) recordValue falls back to a plain step.
			s.g.trace.recordValue(s, n.Pos().Line, assignName, value, targetType)
		}
		s.bindPackedStoreValue(targetType, value)
		if path, ok := s.packedEnumStoragePath(n.Target); ok {
			if err := s.bindPackedStoreOriginsForExprPath(path, n.Value, targetType); err != nil {
				return err
			}
		}
		s.invalidatePackedReadCaches()
		return nil
	case *ast.AsRefAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		s.invalidatePackedEnumStoreOriginExpr(n.Target)
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedVariantViewExpr(n.Target)
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		if err := s.storeValue(ptr, value, targetType, "asref.assign"); err != nil {
			return err
		}
		s.bindPackedStoreValue(targetType, value)
		if path, ok := s.packedEnumStoragePath(n.Target); ok {
			if err := s.bindPackedStoreOriginsForExprPath(path, n.Value, targetType); err != nil {
				return err
			}
		}
		s.invalidatePackedReadCaches()
		return nil
	case *ast.AugAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		current, err := s.loadValue(ptr, targetType, "aug.cur")
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		result, err := s.emitAugmentedValue(n.Op, current, value, targetType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, result, ptr)
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedReadCaches()
		return nil
	case *ast.ReturnStmt:
		if n.Value == nil {
			// docs/90: enforce value-contract `ensure <bool>` postconditions (over params / `old(...)`)
			// on a void return too — the non-void path runs these via emitFunctionReturn, but a bare
			// `return` lowers to RetVoid directly and would otherwise skip them. `result` is void here,
			// so emitPostconditionChecks binds nothing and just emits the boolean checks.
			if err := s.emitPostconditionChecks(nil, nil); err != nil {
				return err
			}
			// docs/85 brick 2 B: enforce `ensures <param> is Law` postconditions on a void return too.
			if err := s.emitRefinementPostconditionChecks(); err != nil {
				return err
			}
			if err := s.emitActiveScopedCleanup(); err != nil {
				return err
			}
			if s.currentBlockTerminated() {
				return nil
			}
			if err := s.emitRegionCleanup(); err != nil {
				return err
			}
			if retUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
				zeroCode, err := s.errorCodeConstant(0)
				if err != nil {
					return err
				}
				// A payloaded error set lowers the void union to a {code, payloads...}
				// struct; wrap the success code into it so the value matches the type.
				successValue, err := s.wrapVoidErrorUnionCode(retUnion.Errors, zeroCode)
				if err != nil {
					return err
				}
				C.LLVMBuildRet(s.builder, successValue)
				return nil
			}
			if s.mainReturnsStatus {
				zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
				C.LLVMBuildRet(s.builder, zero)
			} else {
				C.LLVMBuildRetVoid(s.builder)
			}
			return nil
		}
		// When the function returns a reference (`T&`), pass that as the expected type so
		// a value-typed lvalue (e.g. `return s[h]`) is materialized as the element's
		// address rather than reinterpreted as a pointer. Other return types keep the
		// untyped (nil) path so emitFunctionReturn drives coercion.
		var returnExpected semantic.Type
		if _, ok := s.fnType.Return.(*semantic.RefType); ok {
			returnExpected = s.fnType.Return
		}
		if err := s.emitReturnRefinementChecks(n); err != nil {
			return err
		}
		value, valueType, err := s.emitExpr(n.Value, returnExpected)
		if err != nil {
			return err
		}
		return s.emitFunctionReturn(value, valueType)
	case *ast.BreakStmt:
		if len(s.breakTargets) == 0 {
			return fmt.Errorf("break outside loop during LLVM lowering")
		}
		if err := s.emitLoopExitCleanup(); err != nil {
			return err
		}
		if !s.currentBlockTerminated() {
			C.LLVMBuildBr(s.builder, s.breakTargets[len(s.breakTargets)-1])
		}
		return nil
	case *ast.ContinueStmt:
		if len(s.continueTargets) == 0 {
			return fmt.Errorf("continue outside loop during LLVM lowering")
		}
		if err := s.emitLoopExitCleanup(); err != nil {
			return err
		}
		if !s.currentBlockTerminated() {
			C.LLVMBuildBr(s.builder, s.continueTargets[len(s.continueTargets)-1])
		}
		return nil
	case *ast.IfStmt:
		return s.emitIf(n)
	case *ast.MatchStmt:
		return s.emitMatch(n)
	case *ast.ExpectPatternStmt:
		return s.emitExpectPatternStmt(n)
	case *ast.InStoreStmt:
		return s.emitInStore(n)
	case *ast.CanStmt:
		// A `can Scalar` grant suppresses expected-to-vectorize loop tagging for the block it
		// lexically covers (see functionState.scalarGrantDepth / tagAutovecExpectedLoop).
		if permissionRefsGrantScalar(n.Permissions) {
			s.scalarGrantDepth++
			err := s.emitBlock(n.Body, true)
			s.scalarGrantDepth--
			return err
		}
		return s.emitBlock(n.Body, true)
	case *ast.PoolStmt:
		return s.emitPoolStmt(n)
	case *ast.LockStmt:
		return s.emitLockStmt(n)
	case *ast.WhileStmt:
		return s.emitWhile(n)
	case *ast.ForStmt:
		return s.emitForStmt(n)
	case *ast.IterForStmt:
		return s.emitIterForStmt(n)
	case *ast.ParallelForStmt:
		return s.emitParallelForStmt(n)
	case *ast.PassStmt:
		return nil
	case *ast.SignalStmt:
		return nil
	case *ast.PanicStmt:
		return s.emitPanicWithBacktrace(n.Pos(), n.Message)
	case *ast.ExprStmt:
		if call, ok := n.Expr.(*ast.CallExpr); ok && s.g != nil && s.g.result != nil && s.g.result.LemmaCalls[call] {
			// A lemma call is ghost code: verification already consumed it (requires discharged,
			// ensures injected as facts). Emit nothing.
			return nil
		}
		if s.g != nil && s.g.result != nil && s.g.result.GhostContracts[n.Expr] {
			// An `assert` that reads a ghost var is verification-only — erase its runtime check.
			return nil
		}
		if cond, ok := backendAssertedCondition(n.Expr); ok {
			// A plain `assert(COND)` is NOT a call to a function named `assert` — the analyzer already
			// claimed it (analyzer_flow.go: discharged through the proof ladder, COND recorded as a
			// downstream flow fact). Lower it exactly like `assert … by:`: a debug-gated runtime check,
			// erased at higher opt levels. Emitting it as an ordinary call would fail to resolve `assert`.
			if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
				return nil
			}
			return s.emitContractCheck(cond, "assertion failed")
		}
		_, _, err := s.emitExpr(n.Expr, nil)
		return err
	case *ast.DiscardStmt:
		_, _, err := s.emitExpr(n.Value, nil)
		return err
	case *ast.StaticIfStmt:
		return s.emitStaticIf(n)
	case *ast.StaticErrorStmt:
		return fmt.Errorf("static error should not reach LLVM lowering")
	case *ast.ContractStmt:
		// In-body `invariant` -> a debug-gated runtime check at this point. (Lifted requires/ensure
		// never reach here; a stray non-invariant was already reported by the analyzer.)
		if n.Kind != ast.ContractInvariant || n.Cond == nil {
			return nil
		}
		if s.g != nil && s.g.result != nil && s.g.result.GhostContracts[n.Cond] {
			// A ghost-reading invariant is verification-only (the ghost it reads is erased and has no
			// runtime storage); the obligation was discharged statically. Emit nothing.
			return nil
		}
		if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
			return nil
		}
		if err := s.emitContractCheck(n.Cond, "invariant failed"); err != nil {
			return err
		}
		// Standing invariant: re-asserted after each later mutation of a variable it reads, until its
		// block exits (docs/90 brick 90-14).
		s.registerActiveInvariant(n.Cond)
		return nil
	case *ast.AssertByStmt:
		// The `by:` proof block is verification-only (already consumed by the analyzer) and is ERASED:
		// nothing from it is lowered. COND itself was statically proven; emit it as a debug-gated
		// assertion check, consistent with an ordinary assert (and a no-op at higher opt levels).
		if n.Cond == nil {
			return nil
		}
		if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
			return nil
		}
		return s.emitContractCheck(n.Cond, "assertion failed")
	case *ast.ProofBlockStmt:
		// Standalone proofs are verification-only closed-world regions. The analyzer consumes the
		// proof and exports only the goal as a flow fact; no runtime check or proof body is emitted.
		return nil
	case *ast.AssertHoleStmt:
		return nil
	case *ast.StaticAssertStmt:
		return s.emitStaticAssert(n)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			if err := s.emitStaticAssert(&ast.StaticAssertStmt{Position: item.Position, Cond: item.Cond, Message: item.Message}); err != nil {
				return err
			}
		}
		return nil
	case *ast.StaticBlockStmt:
		return s.emitStaticBlock(n.Body)
	case *ast.MachineCoverageStmt:
		// Compile-time-only: the machine's input coverage was verified in analysis (docs/125
		// §9.1). The real dispatch is the desugared if-ladder; this carries no runtime effect.
		return nil
	default:
		return fmt.Errorf("unsupported statement %T", stmt)
	}
}

// backendAssertedCondition mirrors the analyzer's `assertedCondition`: a plain `assert(COND)` /
// `ASSERT(COND)` is a single-argument call to the reserved `assert` identifier, not a real function
// call. The analyzer already treats it as an assertion; the backend must agree so it lowers to a
// debug-gated check rather than trying (and failing) to resolve `assert` as a callee.
func backendAssertedCondition(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || (ident.Name != "assert" && ident.Name != "ASSERT") {
		return nil, false
	}
	return call.Args[0], true
}
