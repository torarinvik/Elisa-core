//go:build cgo

package backend

// A private, zero-initialized auto arena that never reaches an allocator
// has no storage or hint to release. Examine the complete IR so even an
// allocation or an escape on a single control-flow path preserves cleanup.
func (s *functionState) elideUnusedAutoArenas() {
	if len(s.emptyArenaCandidates) == 0 {
		return
	}
	free := llvmGetNamedFunction(s.g.module, cStringFree("arena_free"))
	for _, arena := range s.emptyArenaCandidates {
		if autoArenaHasOnlyEmptyCleanup(arena, free) {
			for use := llvmGetFirstUse(arena); use != nil; use = llvmGetFirstUse(arena) {
				llvmEraseInstruction(llvmGetUser(use))
			}
			llvmEraseInstruction(arena)
		}
	}
}

func autoArenaHasOnlyEmptyCleanup(arena, free llvmIRValueRef) bool {
	for use := llvmGetFirstUse(arena); use != nil; use = llvmGetNextUse(use) {
		user := llvmGetUser(use)
		if llvmIsStore(user) &&
			llvmGetOperand(user, 1) == arena &&
			!llvmIsVolatile(user) &&
			llvmIsNonAtomic(user) &&
			llvmIsNull(llvmGetOperand(user, 0)) {
			continue
		}
		if free != nil && llvmIsCall(user) &&
			llvmCalledValue(user) == free &&
			llvmNumCallArgs(user) == 1 &&
			llvmGetOperand(user, 0) == arena {
			continue
		}
		return false
	}
	return true
}
