//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitTreeOwnerAllocBytes(owner treeAllocOwnerBinding, byteCount llvmValueRef, name string) (llvmValueRef, error) {
	usizeType := s.g.namedType("usize")
	if owner.arenaRef == nil && owner.arenaRefPtr != nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, name+".owner.arena")
		if err != nil {
			return nil, err
		}
		owner.arenaRef = arenaRef
	}
	if owner.arenaRef == nil {
		return nil, fmt.Errorf("missing Arena owner for allocation")
	}
	arenaType := s.g.namedType("Arena")
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(allocLLVMType, allocCallee, []llvmValueRef{owner.arenaRef, byteCount}, name+".alloc"), nil
}
