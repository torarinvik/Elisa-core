//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBitGroupMemberExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	groupType, ok := s.exprType(expr.Object).(*semantic.BitGroupType)
	if !ok || groupType == nil {
		return nil, nil, false, nil
	}
	member, ok := groupType.MemberMap[expr.Field]
	if !ok {
		return nil, nil, true, fmt.Errorf("%s has no packed member %s", groupType, expr.Field)
	}
	backing, _, err := s.emitExpr(expr.Object, groupType)
	if err != nil {
		return nil, nil, true, err
	}
	value, err := s.extractBitGroupMember(backing, groupType, member, expr.Field)
	return value, member.Type, true, err
}

func (s *functionState) emitBitGroupMemberAssign(target *ast.FieldExpr, valueExpr ast.Expr) (bool, error) {
	groupType, ok := s.exprType(target.Object).(*semantic.BitGroupType)
	if !ok || groupType == nil {
		return false, nil
	}
	member, ok := groupType.MemberMap[target.Field]
	if !ok {
		return true, fmt.Errorf("%s has no packed member %s", groupType, target.Field)
	}
	groupPtr, _, err := s.emitAddress(target.Object)
	if err != nil {
		return true, err
	}
	backingType := s.g.lowerBitInt(groupType.BackingWidth)
	current := C.LLVMBuildLoad2(s.builder, backingType, groupPtr, cStringFree(target.Field+".packed.cur"))
	value, _, err := s.emitExpr(valueExpr, member.Type)
	if err != nil {
		return true, err
	}
	updated, err := s.insertBitGroupMember(current, value, groupType, member, target.Field)
	if err != nil {
		return true, err
	}
	C.LLVMBuildStore(s.builder, updated, groupPtr)
	return true, nil
}

func (s *functionState) extractBitGroupMember(backing C.LLVMValueRef, group *semantic.BitGroupType, member semantic.BitGroupMember, name string) (C.LLVMValueRef, error) {
	backingType := s.g.lowerBitInt(group.BackingWidth)
	shifted := backing
	if member.Offset != 0 {
		shift := C.LLVMConstInt(backingType, C.ulonglong(member.Offset), 0)
		shifted = C.LLVMBuildLShr(s.builder, backing, shift, cStringFree(name+".packed.shift"))
	}
	mask := C.LLVMConstInt(backingType, C.ulonglong(semantic.BitWidthMaxUnsigned(member.Width)), 0)
	masked := C.LLVMBuildAnd(s.builder, shifted, mask, cStringFree(name+".packed.mask"))
	if group.Kind == semantic.BitGroupBitset {
		zero := C.LLVMConstInt(backingType, 0, 0)
		return C.LLVMBuildICmp(s.builder, C.LLVMIntNE, masked, zero, cStringFree(name+".packed.test")), nil
	}
	memberLLVMType, err := s.g.lowerType(member.Type)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildTrunc(s.builder, masked, memberLLVMType, cStringFree(name+".packed.value")), nil
}

func (s *functionState) insertBitGroupMember(current C.LLVMValueRef, value C.LLVMValueRef, group *semantic.BitGroupType, member semantic.BitGroupMember, name string) (C.LLVMValueRef, error) {
	backingType := s.g.lowerBitInt(group.BackingWidth)
	var widened C.LLVMValueRef
	if group.Kind == semantic.BitGroupBitset {
		widened = C.LLVMBuildZExt(s.builder, value, backingType, cStringFree(name+".packed.bool"))
	} else {
		widened = C.LLVMBuildZExt(s.builder, value, backingType, cStringFree(name+".packed.extend"))
	}
	if member.Offset != 0 {
		shift := C.LLVMConstInt(backingType, C.ulonglong(member.Offset), 0)
		widened = C.LLVMBuildShl(s.builder, widened, shift, cStringFree(name+".packed.place"))
	}
	memberMask := semantic.BitWidthMaxUnsigned(member.Width)
	maskBits := memberMask << member.Offset
	mask := C.LLVMConstInt(backingType, C.ulonglong(maskBits), 0)
	clearMask := C.LLVMConstNot(mask)
	cleared := C.LLVMBuildAnd(s.builder, current, clearMask, cStringFree(name+".packed.clear"))
	return C.LLVMBuildOr(s.builder, cleared, C.LLVMBuildAnd(s.builder, widened, mask, cStringFree(name+".packed.limit")), cStringFree(name+".packed.store")), nil
}
