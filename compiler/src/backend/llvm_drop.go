//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// docs/126 phase D1 — compiler-inserted `__drop__` calls for stack values.
//
// A drop-typed local registers a scoped cleanup at its declaration. That single
// registration is all the control flow this feature needs: the existing scopedCleanups
// machinery already fires on EVERY exit edge the RFC names — normal fall-through
// (emitScopeCleanups), `return` and `try` propagation and `raise` (all funnel through
// emitFunctionReturn -> emitActiveScopedCleanup), `break`/`continue` (emitLoopExitCleanup),
// and panic — and it fires in reverse registration order, which for declarations is
// reverse declaration order. Drops therefore inherit correct placement rather than
// re-deriving it.
//
// MOVE SUPPRESSION is dynamic, via a one-bit live flag per value.
//
// docs/126 §8 asks that conditional moves be rejected before a runtime drop flag is
// added. The affine tracker cannot supply that rejection today: mergeAffineValueStates
// (analyzer_flow_value_merges.go) merges consumption with UNION semantics, so a value
// moved on one branch of an `if` reads as consumed on both. A purely static elision
// driven off that state would silently skip a drop that is genuinely still owed on the
// other branch — a leaked fd, exactly what this feature exists to prevent. The flag is
// therefore the sound choice, and it is free in the common case: the flag is an entry
// alloca with constant stores, so mem2reg + simplifycfg fold it and the guarded branch
// away entirely whenever the move is unconditional. Only genuinely conditional moves pay
// for a branch. Revisit if the tracker grows an intersection merge.

// isOwnDropReceiver reports whether paramType's destructor IS the function currently
// being emitted. A destructor must not drop its own receiver: `__drop__` is that value's
// death, so arming a cleanup for `self` inside it would recurse forever.
func (s *functionState) isOwnDropReceiver(paramType semantic.Type) bool {
	hook := semantic.DropHookSymbol(paramType)
	return hook != "" && s.fnType != nil && s.fnType.Name == hook
}

// registerDropCleanup arms the destructor for a freshly declared drop-typed local.
// Returns false when the type has no `__drop__`, in which case nothing is emitted and
// the caller's code path is byte-for-byte what it was before this feature.
func (s *functionState) registerDropCleanup(name string, alloca C.LLVMValueRef, declType semantic.Type) (bool, error) {
	hook := semantic.DropHookSymbol(declType)
	if hook == "" {
		return false, nil
	}
	boolType := s.g.result.NamedTypes["bool"]
	if boolType == nil {
		return false, nil
	}
	// Zero-initialized in the entry block so the flag reads FALSE on any path that
	// somehow reaches a cleanup without having run the declaration. Erring toward "do
	// not drop" makes the failure mode a leak, never a double free.
	flag, err := s.createEntryAllocaZeroed(name+".drop.live", boolType)
	if err != nil {
		return false, err
	}
	llvmBool, err := s.g.lowerType(boolType)
	if err != nil {
		return false, err
	}
	// The value is live from here: arm the flag at the declaration point itself.
	C.LLVMBuildStore(s.builder, C.LLVMConstInt(llvmBool, 1, 0), flag)
	s.registerScopedCleanup(scopedCleanupBinding{
		kind:     scopedCleanupDrop,
		name:     name,
		ptr:      alloca,
		typ:      declType,
		flagPtr:  flag,
		dropHook: hook,
	})
	return true, nil
}

// clearDropLiveFlag disarms a value's destructor. Called when the value is moved away —
// passing it to a consuming callee, returning it, or an explicit `.drop()` — because the
// obligation goes with the value (docs/126 §2 "Moves suppress drops").
func (s *functionState) clearDropLiveFlag(binding scopedCleanupBinding) error {
	if binding.kind != scopedCleanupDrop || binding.flagPtr == nil {
		return nil
	}
	boolType := s.g.result.NamedTypes["bool"]
	llvmBool, err := s.g.lowerType(boolType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, C.LLVMConstInt(llvmBool, 0, 0), binding.flagPtr)
	return nil
}

// emitConditionalDrop is the destructor call itself: `if live { __drop__(move value) }`.
// Modelled on emitConditionalMutexUnlock, which guards its unlock on a null handle for
// the same reason — the slot may already have been given away.
func (s *functionState) emitConditionalDrop(binding scopedCleanupBinding) error {
	if s.currentBlockTerminated() {
		return nil
	}
	if binding.flagPtr == nil || binding.dropHook == "" {
		return nil
	}
	boolType := s.g.result.NamedTypes["bool"]
	llvmBool, err := s.g.lowerType(boolType)
	if err != nil {
		return err
	}
	live := C.LLVMBuildLoad2(s.builder, llvmBool, binding.flagPtr, cStringFree("drop.live"))
	isLive := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), live, C.LLVMConstInt(llvmBool, 0, 0), cStringFree("drop.islive"))
	dropBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("drop.run"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("drop.after"))
	C.LLVMBuildCondBr(s.builder, isLive, dropBB, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, dropBB)
	// Synthesize the call through the ordinary expression emitter so the destructor gets
	// the same ABI, byval and sret handling as any hand-written call. The `move` also
	// routes through emitMovedValue, which zeroes the slot and clears this very flag —
	// so a `__drop__` body that itself exits cannot re-enter this drop.
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: binding.dropHook},
		Args: []ast.Expr{&ast.MoveExpr{Operand: &ast.Ident{Name: binding.name}}},
	}
	if _, _, err := s.emitExpr(call, nil); err != nil {
		return err
	}
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, contBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}
