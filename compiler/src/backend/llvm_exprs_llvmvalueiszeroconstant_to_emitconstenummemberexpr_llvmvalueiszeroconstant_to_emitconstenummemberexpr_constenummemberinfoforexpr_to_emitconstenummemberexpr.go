//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int llcontextLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef llctxAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef llctxAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned llctxMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void llctxSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = llctxAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, llctxMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef llctxCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = llctxAliasMDString(ctx, domainName);
	return llctxAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef llctxCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = llctxAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return llctxAliasMDNode(ctx, operands, 2);
}

static void llctxAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = llctxCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = llctxCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	llctxSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = llctxCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = llctxCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		llctxSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/semantic"
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
		base, ok := s.g.result.NamedTypes[baseName]
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
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, false
		}
		constEnumType, ok := base.(*semantic.ConstEnumType)
		return constEnumType, ok
	case *ast.FieldExpr:
		if kindType, ok := s.treeCategoryKindTypeForExpr(n); ok {
			return kindType, true
		}
		ident, ok := n.Object.(*ast.Ident)
		if !ok || n.Field != "Tag" {
			return nil, false
		}
		base, ok := s.g.result.NamedTypes[ident.Name]
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
func (s *functionState) treeCategoryKindTypeForExpr(expr *ast.FieldExpr) (*semantic.ConstEnumType, bool) {
	if expr == nil || expr.Field != "Kind" {
		return nil, false
	}
	ownerName, _, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, false
	}
	base, ok := s.g.result.NamedTypes[ownerName]
	if !ok {
		return nil, false
	}
	categoryType, ok := base.(*semantic.TreeCategoryType)
	if !ok || categoryType == nil || categoryType.KindType == nil {
		return nil, false
	}
	return categoryType.KindType, true
}
func (s *functionState) treeTypeForExpr(expr ast.Expr) (*semantic.TreeType, bool) {
	if expr == nil {
		return nil, false
	}
	if treeType, ok := s.exprType(expr).(*semantic.TreeType); ok {
		return treeType, true
	}
	switch n := expr.(type) {
	case *ast.Ident:
		base, ok := s.g.result.NamedTypes[n.Name]
		if !ok {
			return nil, false
		}
		treeType, ok := base.(*semantic.TreeType)
		return treeType, ok
	case *ast.ParenExpr:
		return s.treeTypeForExpr(n.Inner)
	default:
		return nil, false
	}
}
func (s *functionState) emitConstEnumMemberExpr(constEnumType *semantic.ConstEnumType, member *semantic.ConstEnumMember) (C.LLVMValueRef, semantic.Type, error) {
	if constEnumType == nil || member == nil {
		return nil, nil, fmt.Errorf("missing const enum member metadata")
	}
	llvmType, err := s.g.lowerType(constEnumType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(llvmType, C.ulonglong(member.Value), boolToLLVMBool(member.Value < 0)), constEnumType, nil
}
