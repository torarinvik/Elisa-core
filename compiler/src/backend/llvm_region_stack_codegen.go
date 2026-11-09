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
	"elisacore/src/semantic"
	"fmt"
	"reflect"
)

func (s *functionState) emitScopedArenaStmt(n *ast.RegionStmt) error {
	s.pushScope()
	scope := s.scope
	defer s.popScope()
	savedTreeOwner := s.treeAllocOwner
	savedSynthesizedAutoRegion := s.activeSynthesizedAutoRegion
	s.activeSynthesizedAutoRegion = backendSynthesizedAutoRegionActive(savedSynthesizedAutoRegion, n)
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
		s.defineBinding(n.Name, valueBinding{ptr: s.regionPolyOwner.arenaRef, typ: s.g.namedType("Arena")})
		s.treeAllocOwner = s.regionPolyOwner
		s.prepareAdoptedEscapeNames()
		if err := s.ensureAdoptedScratchArena(); err != nil {
			return err
		}
		if assignment, ok := s.g.semanticReadView().LookupRegionStacks(n); ok && assignment.StackCount > 1 {
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
	usizeType := s.g.namedType("usize")
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
	intType := s.g.namedType("int")
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
	asn, ok := s.g.semanticReadView().LookupRegionStacks(n)
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
	arenaType := s.g.namedType("Arena")
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
				if initErr == nil && !loopReset && asn.StackBlockBound[k] != nil {
					initErr = s.emitStackBlockInit(alloca, arenaType, asn.StackBlockBound[k], asn.StackElemType[k])
				}
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
	// statement at its recorded offset. Stacks whose objects die after the SAME statement are
	// all freed there (the analyzer counts them as one lifetime class), so the entry is a list,
	// filled in stack-id order. A single slot kept whichever stack the StackEarlyFreeAfter map
	// walk reached last: the choice changed from run to run, and the other stack waited for the
	// region exit. This region's list replaces an enclosing region's entry for the same offset
	// rather than joining it; the exit restores that entry, so the enclosing arenas still wait
	// for the enclosing statement.
	if s.earlyFreeByOffset == nil {
		s.earlyFreeByOffset = map[int][]C.LLVMValueRef{}
	}
	type prevFree struct {
		value []C.LLVMValueRef
		had   bool
	}
	savedFree := map[int]prevFree{}
	for k := 1; k < asn.StackCount; k++ {
		off, early := asn.StackEarlyFreeAfter[k]
		alloca, allocated := allocaByStack[k]
		if !early || !allocated {
			continue
		}
		if _, saved := savedFree[off]; !saved {
			v, had := s.earlyFreeByOffset[off]
			savedFree[off] = prevFree{value: v, had: had}
			s.earlyFreeByOffset[off] = nil
		}
		s.earlyFreeByOffset[off] = append(s.earlyFreeByOffset[off], alloca)
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
				for _, i := range astWalkFields(v.Type()) {
					collectNames(v.Field(i))
				}
			case reflect.Slice, reflect.Array:
				if !astTypeMayHoldNodes(v.Type().Elem()) {
					break
				}
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
			for _, i := range astWalkFields(v.Type()) {
				walkExpr(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			if !astTypeMayHoldNodes(v.Type().Elem()) {
				break
			}
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
			for _, i := range astWalkFields(v.Type()) {
				walkStmt(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			if !astTypeMayHoldNodes(v.Type().Elem()) {
				break
			}
			for i := 0; i < v.Len(); i++ {
				walkStmt(v.Index(i))
			}
		}
	}
	walkStmt(reflect.ValueOf(region.Body))
	for changed := true; changed; {
		changed = false
		for owner, sources := range dependencies {
			// An owner outside the region's stacks (a parameter such as `out` in
			// `out.push(e)`, or an enclosing local) outlives the region, so what is copied
			// into it escapes.
			ownerStack, ownerKnown := asn.StackOf[owner]
			if ownerKnown && !escaped[ownerStack] {
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
