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
		alloca, err = s.createEntryAlloca(n.Name, arenaType)
		if err != nil {
			return err
		}
		zero, zerr := s.zeroValue(arenaType)
		if zerr != nil {
			return zerr
		}
		C.LLVMBuildStore(s.builder, zero, alloca)
	}
	s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: arenaType})
	s.regions = append(s.regions, regionBinding{name: n.Name, ptr: alloca, typ: arenaType})
	s.treeAllocOwner = treeAllocOwnerBinding{arenaRef: alloca}
	// A lazy region (e.g. `in auto:`) is left zero-initialized: arena_alloc creates its
	// first block on demand, and arena_free over a never-allocated arena is a no-op. So a
	// region that never allocates costs nothing beyond the stack slot.
	if n.Lazy {
		return nil
	}
	return s.emitRegionInit(alloca, arenaType, n.Capacity, n.Allocator)
}
func (s *functionState) emitScopedArenaStmt(n *ast.RegionStmt) error {
	s.pushScope()
	scope := s.scope
	defer s.popScope()
	savedTreeOwner := s.treeAllocOwner
	defer func() {
		s.treeAllocOwner = savedTreeOwner
	}()
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
	twoN := C.LLVMBuildMul(s.builder, nValue, C.LLVMConstInt(usizeLLVM, 2, 0), cStringFree("rc.2n"))
	bytes := C.LLVMBuildMul(s.builder, twoN, C.LLVMConstInt(usizeLLVM, C.ulonglong(elemBytes), 0), cStringFree("rc.bytes"))
	bytes = C.LLVMBuildAdd(s.builder, bytes, C.LLVMConstInt(usizeLLVM, 4096, 0), cStringFree("rc.bytes.headroom"))
	slots := C.LLVMBuildUDiv(s.builder, bytes, C.LLVMConstInt(usizeLLVM, 8, 0), cStringFree("rc.slots"))
	slots = C.LLVMBuildAdd(s.builder, slots, C.LLVMConstInt(usizeLLVM, 8, 0), cStringFree("rc.slots.pad"))
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
			alloca, err = s.createEntryAlloca(name, arenaType)
			if err != nil {
				return noop, err
			}
			zero, zerr := s.zeroValue(arenaType)
			if zerr != nil {
				return noop, zerr
			}
			C.LLVMBuildStore(s.builder, zero, alloca)
		}
		// Lazy arena: first block allocated on demand, free is a no-op if unused -> an unused
		// parallel stack costs only the stack slot.
		allocaByStack[k] = alloca
		s.regions = append(s.regions, regionBinding{name: name, ptr: alloca, typ: arenaType})
		kind := scopedCleanupRegion
		if loopReset {
			kind = scopedCleanupRegionReset
		}
		s.registerScopedCleanup(scopedCleanupBinding{kind: kind, name: name, ptr: alloca, typ: arenaType})
		// Phase C1c: a reserve_commit stack is eagerly initialized with a contiguous reservation
		// sized to its proven element bound, so the base never moves and interior refs survive
		// growth. Skipped under loopReset (a per-iteration reset region) — chained stays correct
		// there. The strategy is only assigned when the footprint is provably <= N (docs/72), so
		// the reservation can never overflow.
		if !loopReset && asn.StackStrategy[k] == "reserve_commit" {
			var initErr error
			if asn.StackCapacity[k] != nil {
				// Bounded (Phase C): reservation sized to the proven footprint.
				initErr = s.emitReserveCommitStackInit(alloca, arenaType, asn.StackCapacity[k], asn.StackElemType[k])
			} else {
				// Default (docs/73): no inferred bound, reserve a fixed generous range.
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
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: declType, mutable: n.Mutable})
		if s.g.di != nil {
			s.g.di.declareVariable(s, n.Name, alloca, declType, n.Pos().Line, 0)
		}
		var initValue C.LLVMValueRef
		if n.Value != nil {
			value, _, err := s.emitExpr(n.Value, declType)
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
		s.bindImplicitTreeOwnerParam(n.Name, declType, alloca, initValue)
		if s.g.trace != nil {
			s.g.trace.recordValue(s, n.Pos().Line, n.Name, initValue, declType)
		}
		return nil
	case *ast.LetDestructureStmt:
		return s.emitLetDestructureStmt(n)
	case *ast.TupleBindStmt:
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
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		if err := s.storeValue(ptr, value, targetType, "assign"); err != nil {
			return err
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
	case *ast.LocalParamsStmt:
		return nil
	case *ast.ReturnStmt:
		if n.Value == nil {
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
			C.LLVMBuildRetVoid(s.builder)
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
		return s.emitBlock(n.Body, true)
	case *ast.WithStmt:
		return s.emitBlock(n.Body, true)
	case *ast.ArgsScopeStmt:
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
		_, _, err := s.emitExpr(n.Expr, nil)
		return err
	case *ast.DiscardStmt:
		_, _, err := s.emitExpr(n.Value, nil)
		return err
	case *ast.StaticIfStmt:
		return s.emitStaticIf(n)
	case *ast.StaticErrorStmt:
		return fmt.Errorf("static error should not reach LLVM lowering")
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
	default:
		return fmt.Errorf("unsupported statement %T", stmt)
	}
}
