//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

func (s *functionState) constEnumMemberInfoForExpr(expr ast.Expr) (*semantic.ConstEnumType, string, *semantic.ConstEnumMember, bool) {
	parts, ok := qualifiedFieldParts(expr)
	if !ok || len(parts) < 2 {
		return nil, "", nil, false
	}
	fullName := strings.Join(parts, ".")
	if !ok || fullName == "" {
		return nil, "", nil, false
	}
	var matchedType *semantic.ConstEnumType
	var matchedMemberName string
	for i := len(parts) - 1; i >= 1; i-- {
		baseName := strings.Join(parts[:i], ".")
		base, _, ok := s.lookupVisibleNamedType(baseName)
		if !ok {
			continue
		}
		constEnumType, ok := base.(*semantic.ConstEnumType)
		if !ok || constEnumType == nil {
			continue
		}
		memberName := strings.Join(parts[i:], ".")
		if matchedType == nil {
			matchedType = constEnumType
			matchedMemberName = memberName
		}
		if member, ok := constEnumType.Member(memberName); ok {
			return constEnumType, memberName, member, true
		}
	}
	if matchedType != nil {
		return matchedType, matchedMemberName, nil, true
	}
	return nil, "", nil, false
}
func (s *functionState) constEnumTypeForExpr(expr ast.Expr) (*semantic.ConstEnumType, bool) {
	if expr == nil {
		return nil, false
	}
	if constEnumType, ok := s.exprType(expr).(*semantic.ConstEnumType); ok {
		return constEnumType, true
	}
	switch n := expr.(type) {
	case *ast.Ident:
		base, _, ok := s.lookupVisibleNamedType(n.Name)
		if !ok {
			return nil, false
		}
		constEnumType, ok := base.(*semantic.ConstEnumType)
		return constEnumType, ok
	case *ast.FieldExpr:
		ident, ok := n.Object.(*ast.Ident)
		if !ok || n.Field != "Tag" {
			return nil, false
		}
		base, _, ok := s.lookupVisibleNamedType(ident.Name)
		if !ok {
			return nil, false
		}
		enumType, ok := base.(*semantic.EnumType)
		if !ok || !enumType.Packed || enumType.TagType == nil {
			return nil, false
		}
		return enumType.TagType, true
	case *ast.ParenExpr:
		return s.constEnumTypeForExpr(n.Inner)
	default:
		return nil, false
	}
}
func shorthandMemberNameBackend(expr *ast.ShorthandMemberExpr) string {
	if expr == nil {
		return ""
	}
	return strings.Join(expr.Parts, ".")
}
func shorthandMemberDisplayBackend(expr *ast.ShorthandMemberExpr) string {
	if expr == nil {
		return ".<invalid>"
	}
	return "." + shorthandMemberNameBackend(expr)
}
func (s *functionState) shorthandConstEnumMemberInfo(expr *ast.ShorthandMemberExpr) (*semantic.ConstEnumType, *semantic.ConstEnumMember, bool) {
	if expr == nil {
		return nil, nil, false
	}
	constEnumType, ok := s.exprType(expr).(*semantic.ConstEnumType)
	if !ok || constEnumType == nil {
		return nil, nil, false
	}
	member, ok := constEnumType.Member(shorthandMemberNameBackend(expr))
	if !ok || member == nil {
		return nil, nil, false
	}
	return constEnumType, member, true
}

func (s *functionState) emitConstEnumMemberExpr(constEnumType *semantic.ConstEnumType, member *semantic.ConstEnumMember) (llvmIRValueRef, semantic.Type, error) {
	if constEnumType == nil || member == nil {
		return nil, nil, fmt.Errorf("missing const enum member metadata")
	}
	llvmType, err := s.g.lowerType(constEnumType)
	if err != nil {
		return nil, nil, err
	}
	return llvmConstInt(llvmType, llvmIRUnsignedLongLong(member.Value), boolToLLVMBool(member.Value < 0)), constEnumType, nil
}
