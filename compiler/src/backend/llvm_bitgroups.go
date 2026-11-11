//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBitGroupMemberExpr(expr *ast.FieldExpr) (llvmIRValueRef, semantic.Type, bool, error) {
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
	current := llvmBuildLoad2(s.builder, backingType, groupPtr, cStringFree(target.Field+".packed.cur"))
	value, _, err := s.emitExpr(valueExpr, member.Type)
	if err != nil {
		return true, err
	}
	updated, err := s.insertBitGroupMember(current, value, groupType, member, target.Field)
	if err != nil {
		return true, err
	}
	llvmBuildStore(s.builder, updated, groupPtr)
	return true, nil
}

func (s *functionState) extractBitGroupMember(backing llvmIRValueRef, group *semantic.BitGroupType, member semantic.BitGroupMember, name string) (llvmIRValueRef, error) {
	backingType := s.g.lowerBitInt(group.BackingWidth)
	shifted := backing
	if member.Offset != 0 {
		shift := llvmConstInt(backingType, llvmIRUnsignedLongLong(member.Offset), 0)
		shifted = llvmBuildLShr(s.builder, backing, shift, cStringFree(name+".packed.shift"))
	}
	mask := llvmConstInt(backingType, llvmIRUnsignedLongLong(semantic.BitWidthMaxUnsigned(member.Width)), 0)
	masked := llvmBuildAnd(s.builder, shifted, mask, cStringFree(name+".packed.mask"))
	if group.Kind == semantic.BitGroupBitset {
		zero := llvmConstInt(backingType, 0, 0)
		return llvmBuildICmp(s.builder, llvmIntNE, masked, zero, cStringFree(name+".packed.test")), nil
	}
	memberLLVMType, err := s.g.lowerType(member.Type)
	if err != nil {
		return nil, err
	}
	return llvmBuildTrunc(s.builder, masked, memberLLVMType, cStringFree(name+".packed.value")), nil
}

func (s *functionState) insertBitGroupMember(current llvmIRValueRef, value llvmIRValueRef, group *semantic.BitGroupType, member semantic.BitGroupMember, name string) (llvmIRValueRef, error) {
	backingType := s.g.lowerBitInt(group.BackingWidth)
	var widened llvmIRValueRef
	if group.Kind == semantic.BitGroupBitset {
		widened = llvmBuildZExt(s.builder, value, backingType, cStringFree(name+".packed.bool"))
	} else {
		widened = llvmBuildZExt(s.builder, value, backingType, cStringFree(name+".packed.extend"))
	}
	if member.Offset != 0 {
		shift := llvmConstInt(backingType, llvmIRUnsignedLongLong(member.Offset), 0)
		widened = llvmBuildShl(s.builder, widened, shift, cStringFree(name+".packed.place"))
	}
	memberMask := semantic.BitWidthMaxUnsigned(member.Width)
	maskBits := memberMask << member.Offset
	mask := llvmConstInt(backingType, llvmIRUnsignedLongLong(maskBits), 0)
	clearMask := llvmConstNot(mask)
	cleared := llvmBuildAnd(s.builder, current, clearMask, cStringFree(name+".packed.clear"))
	return llvmBuildOr(s.builder, cleared, llvmBuildAnd(s.builder, widened, mask, cStringFree(name+".packed.limit")), cStringFree(name+".packed.store")), nil
}
