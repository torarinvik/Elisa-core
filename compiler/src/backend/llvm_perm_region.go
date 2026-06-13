package backend

/*
#include "llvm-c/Core.h"
*/
import "C"

import (
	"fmt"

	"elisacore/src/ast"
)

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

// permGrowthOwner returns the perm arena owner when the semantic analyzer marked
// this growth op (push/extend/reserve/resize/put/...) as program-lifetime — its
// receiver roots at a global, so its backing belongs in the permanent region.
// This is the in-place analogue of routing a global store's RHS to perm.
func (s *functionState) permGrowthOwner(expr ast.Expr) (treeAllocOwnerBinding, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || s.g.result.PermGrowthOps == nil {
		return treeAllocOwnerBinding{}, false, nil
	}
	if !s.g.result.PermGrowthOps[expr] {
		return treeAllocOwnerBinding{}, false, nil
	}
	owner, err := s.permTreeAllocOwner()
	if err != nil {
		return treeAllocOwnerBinding{}, false, err
	}
	return owner, true, nil
}
