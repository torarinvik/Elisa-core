package backend

/*
#include "llvm-c/Core.h"
*/
import "C"

import "fmt"

// permArenaGlobalName is the module-level Arena backing the `perm` program-lifetime
// region. It is zero-initialized (a lazy arena: arena_alloc creates the first block
// on demand) and never freed — perm allocations live until process exit, exactly
// like static data. `in perm:` blocks and global-store region inference route here.
const permArenaGlobalName = "__elisa_perm_arena"

// permArenaGlobal lazily defines (once per module) the Arena global that backs the
// perm region and returns a pointer to it.
func (g *llvmGenerator) permArenaGlobal() (C.LLVMValueRef, error) {
	if value, ok := g.globals[permArenaGlobalName]; ok {
		return value, nil
	}
	arenaType := g.result.NamedTypes["Arena"]
	if arenaType == nil {
		return nil, fmt.Errorf("missing builtin Arena type for perm region")
	}
	value, err := g.addGlobal(permArenaGlobalName, arenaType, false)
	if err != nil {
		return nil, err
	}
	zero, err := g.constZero(arenaType)
	if err != nil {
		return nil, err
	}
	C.LLVMSetInitializer(value, zero)
	C.LLVMSetLinkage(value, C.LLVMInternalLinkage)
	g.globals[permArenaGlobalName] = value
	return value, nil
}

// permTreeAllocOwner resolves the perm region as an allocation owner for a
// functionState (container literals, darray growth, region-poly threading).
func (s *functionState) permTreeAllocOwner() (treeAllocOwnerBinding, error) {
	global, err := s.g.permArenaGlobal()
	if err != nil {
		return treeAllocOwnerBinding{}, err
	}
	return treeAllocOwnerBinding{arenaRef: global}, nil
}
