//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitTernaryExpr(expr *ast.TernaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	resultType := s.exprType(expr)
	parentScope := s.scope
	condScope, hasConditionBindings, err := s.createConditionBindingScope(expr.Cond)
	if err != nil {
		return nil, nil, err
	}
	if hasConditionBindings {
		s.scope = condScope
	}
	parentBlock := C.LLVMGetInsertBlock(s.builder)
	thenBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.then"))
	elseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.else"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("ternary.end"))
	if err := s.emitConditionBranchWithBindings(expr.Cond, thenBB, elseBB, ast.BranchHintNone); err != nil {
		s.scope = parentScope
		return nil, nil, err
	}

	C.LLVMPositionBuilderAtEnd(s.builder, thenBB)
	if hasConditionBindings {
		s.scope = condScope
	}
	leftValue, _, err := s.emitExpr(expr.Value, resultType)
	if err != nil {
		s.scope = parentScope
		return nil, nil, err
	}
	thenEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(thenEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, elseBB)
	s.scope = parentScope
	rightValue, _, err := s.emitExpr(expr.Alt, resultType)
	if err != nil {
		s.scope = parentScope
		return nil, nil, err
	}
	elseEnd := C.LLVMGetInsertBlock(s.builder)
	if C.LLVMGetBasicBlockTerminator(elseEnd) == nil {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	s.scope = parentScope
	phiType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, phiType, cStringFree("termp"))
	values := []C.LLVMValueRef{leftValue, rightValue}
	blocks := []C.LLVMBasicBlockRef{thenEnd, elseEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	_ = parentBlock
	return phi, resultType, nil
}
func (s *functionState) emitAddrOfExpr(expr *ast.AddrOfExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, operandType, err := s.emitAddress(expr.Operand)
	if err != nil {
		return nil, nil, err
	}
	return ptr, &semantic.RefType{Elem: operandType, State: semantic.RefStateNonNull}, nil
}
func (s *functionState) emitSpecializeExpr(expr *ast.SpecializeExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Operand == nil {
		return nil, nil, fmt.Errorf("missing specialization operand")
	}
	ident, ok := expr.Operand.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("specialize expects a named generic function")
	}
	sym, ok := s.g.result.GlobalScope.Lookup(ident.Name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown generic function %q during LLVM lowering", ident.Name)
	}
	baseType, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		return nil, nil, fmt.Errorf("specialize expects a function, got %s", sym.Type.String())
	}
	params := funcGenericParams(baseType)
	if len(params) == 0 {
		return nil, nil, fmt.Errorf("function %q is not generic", ident.Name)
	}
	if len(expr.TypeArgs) != len(params) {
		return nil, nil, fmt.Errorf("function %q expects %d arguments, got %d", ident.Name, len(params), len(expr.TypeArgs))
	}
	bindings := make(map[string]semantic.Type, len(params))
	for i, arg := range expr.TypeArgs {
		resolved, err := s.resolveGenericArgForParam(arg, params[i])
		if err != nil {
			return nil, nil, err
		}
		bindings[params[i].Name] = resolved
	}
	specialized := specializeFuncType(baseType, bindings, s.g.result.StaticImpls)
	if decl, ok := sym.Node.(*ast.FuncDecl); ok {
		value, lowered, err := s.g.ensureSpecializedFunction(decl, baseType, bindings)
		return value, lowered, err
	}
	value, err := s.g.ensureFunctionDeclared(ident.Name, specialized)
	return value, specialized, err
}
func (s *functionState) emitStructLitExpr(expr *ast.StructLitExpr) (C.LLVMValueRef, semantic.Type, error) {
	if s != nil && s.g != nil && s.g.result != nil && s.g.result.InitCalls != nil {
		if call, ok := s.g.result.InitCalls[expr]; ok && call != nil {
			return s.emitExpr(call, s.exprType(expr))
		}
	}
	structType := s.exprType(expr)
	llvmType, err := s.g.lowerType(structType)
	if err != nil {
		return nil, nil, err
	}
	fields, err := s.g.structLiteralFields(structType)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	args := expr.LoweredArgs()
	for i, arg := range args {
		if i >= len(fields) {
			break
		}
		if arg == nil {
			return nil, nil, fmt.Errorf("struct literal field %d was not resolved", i)
		}
		fieldValue, _, err := s.emitExpr(arg, fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, fieldValue, C.unsigned(i), cStringFree("ins"))
	}
	return value, structType, nil
}
func (s *functionState) emitRecordUpdateExpr(expr *ast.RecordUpdateExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Base == nil {
		return nil, nil, fmt.Errorf("invalid record update")
	}
	memberType := semantic.StripAggregateStateType(s.exprType(expr.Base))
	if exactType, ok := s.rewriteDefaultExactMemberType(expr.Base); ok {
		memberType = exactType
	}
	if _, exact := semantic.TreeExactTag(memberType); exact {
		value, _, err := s.emitTreeExactMemberUpdateExpr(expr, memberType, nil)
		return value, s.exprType(expr), err
	}
	baseValue, baseType, err := s.emitExpr(expr.Base, nil)
	if err != nil {
		return nil, nil, err
	}
	fields, err := s.g.structLiteralFields(baseType)
	if err != nil {
		return nil, nil, err
	}
	value := baseValue
	args := expr.LoweredArgs()
	for i, field := range fields {
		if i >= len(args) || args[i] == nil {
			continue
		}
		fieldValue, _, err := s.emitExpr(args[i], field.Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, fieldValue, C.unsigned(field.Index), cStringFree("record.update"))
	}
	return value, baseType, nil
}
func (s *functionState) rewriteDefaultExactMemberType(expr ast.Expr) (semantic.Type, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name != "default" || s.treeRewriteDefault == nil || s.treeRewriteDefault.memberType == nil {
			return nil, false
		}
		return semantic.StripAggregateStateType(s.treeRewriteDefault.memberType), true
	case *ast.ParenExpr:
		return s.rewriteDefaultExactMemberType(n.Inner)
	default:
		return nil, false
	}
}
func (s *functionState) emitTreeExactMemberUpdateExpr(expr *ast.RecordUpdateExpr, memberType semantic.Type, owner *treeAllocOwnerBinding) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Base == nil {
		return nil, nil, fmt.Errorf("invalid tree update")
	}
	memberType = semantic.StripAggregateStateType(memberType)
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, nil, fmt.Errorf("missing tree family metadata for update %s", memberType.String())
	}
	tag, ok := treeExactMemberTag(memberType)
	if !ok {
		return nil, nil, fmt.Errorf("missing exact tree member tag for update %s", memberType.String())
	}
	handleValue, baseType, err := s.emitTreeHandleValue(expr.Base, s.exprType(expr.Base))
	if err != nil {
		return nil, nil, err
	}
	if handleValue == nil || baseType == nil {
		return nil, nil, fmt.Errorf("tree update requires an exact tree member base")
	}
	if resolvedBaseType := semantic.StripAggregateStateType(baseType); resolvedBaseType != nil {
		if _, exact := semantic.TreeExactTag(resolvedBaseType); exact {
			memberType = resolvedBaseType
		}
	}
	resolvedOwner := treeAllocOwnerBinding{}
	if owner != nil {
		resolvedOwner = *owner
	} else {
		activeOwner, ok := s.lookupTreeAllocOwner()
		if !ok {
			return nil, nil, fmt.Errorf("tree update of %s requires an active in <owner>: scope or explicit new[owner]", memberType.String())
		}
		resolvedOwner = activeOwner
	}
	fieldDecls := treeExactFieldDecls(memberType)
	orderedArgs := expr.LoweredArgs()
	if len(orderedArgs) == 0 {
		orderedArgs = make([]ast.Expr, len(fieldDecls))
	}
	storeValue, _, err := s.ensureTreeOwnerStoreValue(resolvedOwner, family)
	if err != nil {
		return nil, nil, err
	}
	if viewType, ok := semantic.StripAggregateStateType(memberType).(*semantic.TreeVariantViewType); ok && viewType != nil && viewType.Category != nil && viewType.Category.Layout == semantic.TreeLayoutCategoryUnion {
		sourceAccess, err := s.emitTreeCategoryUnionTableAccessFromHandle(handleValue, family, viewType.Category, "tree.update.src")
		if err != nil {
			return nil, nil, err
		}
		arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.update.store.arena")
		stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.update.store.state")
		slot, err := s.emitTreeCategoryUnionAppendSlot(arenaValue, stateValue, family, viewType.Category, "tree.update")
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeCategoryUnionKindAtIndex(slot.tablePtr, viewType.Category, slot.rowIndex, tag, "tree.update"); err != nil {
			return nil, nil, err
		}
		patchNames := make([]string, 0, len(orderedArgs))
		patchValues := make([]C.LLVMValueRef, 0, len(orderedArgs))
		for i, fieldDecl := range fieldDecls {
			if i >= len(orderedArgs) || orderedArgs[i] == nil {
				continue
			}
			field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
			if !ok {
				return nil, nil, fmt.Errorf("missing exact tree field %s.%s", memberType.String(), fieldDecl.Name)
			}
			fieldValue, _, err := s.emitExpr(orderedArgs[i], field.Type)
			if err != nil {
				return nil, nil, err
			}
			patchNames = append(patchNames, fieldDecl.Name)
			patchValues = append(patchValues, fieldValue)
		}
		if err := s.emitTreeCopyCategoryUnionPayloadAndPatch(sourceAccess.tablePtr, slot.tablePtr, viewType.Category, viewType.Variant, sourceAccess.rowIndex, slot.rowIndex, patchNames, patchValues, "tree.update"); err != nil {
			return nil, nil, err
		}
		if err := s.emitTreeCategoryUnionTableSetCount(slot.tablePtr, viewType.Category, slot.neededCount, "tree.update"); err != nil {
			return nil, nil, err
		}
		keyValue, err := s.buildTreeHandleKey(tag, slot.rowIndex, "tree.update")
		if err != nil {
			return nil, nil, err
		}
		handleValue, err = s.buildTreeHandleValue(family, stateValue, keyValue, "tree.update")
		if err != nil {
			return nil, nil, err
		}
		return handleValue, memberType, nil
	}
	sourceAccess, err := s.emitTreeExactTableAccessFromHandle(handleValue, family, memberType, "tree.update.src")
	if err != nil {
		return nil, nil, err
	}
	arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.update.store.arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.update.store.state")
	slot, err := s.emitTreeExactAppendSlot(arenaValue, stateValue, family, memberType, "tree.update")
	if err != nil {
		return nil, nil, err
	}
	if len(fieldDecls) > 0 {
		rowValue, _, err := s.emitTreeExactRowValueAtIndex(sourceAccess.tablePtr, memberType, sourceAccess.rowIndex, "tree.update.src")
		if err != nil {
			return nil, nil, err
		}
		patchNames := make([]string, 0, len(orderedArgs))
		patchValues := make([]C.LLVMValueRef, 0, len(orderedArgs))
		for i, fieldDecl := range fieldDecls {
			if i >= len(orderedArgs) || orderedArgs[i] == nil {
				continue
			}
			field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
			if !ok {
				return nil, nil, fmt.Errorf("missing exact tree field %s.%s", memberType.String(), fieldDecl.Name)
			}
			fieldValue, _, err := s.emitExpr(orderedArgs[i], field.Type)
			if err != nil {
				return nil, nil, err
			}
			patchNames = append(patchNames, fieldDecl.Name)
			patchValues = append(patchValues, fieldValue)
		}
		if err := s.emitTreeCopyRowAndPatch(slot.tablePtr, memberType, slot.rowIndex, rowValue, patchNames, patchValues, "tree.update"); err != nil {
			return nil, nil, err
		}
	}
	if err := s.emitTreeTableSetCount(slot.tablePtr, memberType, slot.neededCount, "tree.update"); err != nil {
		return nil, nil, err
	}
	keyValue, err := s.buildTreeHandleKey(tag, slot.rowIndex, "tree.update")
	if err != nil {
		return nil, nil, err
	}
	handleValue, err = s.buildTreeHandleValue(family, stateValue, keyValue, "tree.update")
	if err != nil {
		return nil, nil, err
	}
	return handleValue, memberType, nil
}
