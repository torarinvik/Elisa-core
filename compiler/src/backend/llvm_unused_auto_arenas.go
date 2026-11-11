package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

// A private, zero-initialized auto arena that never reaches an allocator
// has no storage or hint to release. Examine the complete IR so even an
// allocation or an escape on a single control-flow path preserves cleanup.
func (s *functionState) elideUnusedAutoArenas() {
	if len(s.emptyArenaCandidates) == 0 {
		return
	}
	free := C.LLVMGetNamedFunction(s.g.module, cStringFree("arena_free"))
	for _, arena := range s.emptyArenaCandidates {
		if autoArenaHasOnlyEmptyCleanup(arena, free) {
			for use := C.LLVMGetFirstUse(arena); use != nil; use = C.LLVMGetFirstUse(arena) {
				C.LLVMInstructionEraseFromParent(C.LLVMGetUser(use))
			}
			C.LLVMInstructionEraseFromParent(arena)
		}
	}
}

func autoArenaHasOnlyEmptyCleanup(arena, free C.LLVMValueRef) bool {
	for use := C.LLVMGetFirstUse(arena); use != nil; use = C.LLVMGetNextUse(use) {
		user := C.LLVMGetUser(use)
		if C.LLVMIsAStoreInst(user) != nil &&
			C.LLVMGetOperand(user, 1) == arena &&
			C.LLVMGetVolatile(user) == 0 &&
			C.LLVMGetOrdering(user) == C.LLVMAtomicOrderingNotAtomic &&
			C.LLVMIsNull(C.LLVMGetOperand(user, 0)) != 0 {
			continue
		}
		if free != nil && C.LLVMIsACallInst(user) != nil &&
			C.LLVMGetCalledValue(user) == free &&
			C.LLVMGetNumArgOperands(user) == 1 &&
			C.LLVMGetOperand(user, 0) == arena {
			continue
		}
		return false
	}
	return true
}
