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
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

func (s *functionState) emitPointerCompareExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr.Op != lexer.TOKEN_EQEQ && expr.Op != lexer.TOKEN_BANGEQ {
		return nil, nil, false, nil
	}
	leftPointerish := isPointerLikeType(leftType) || semantic.IsNullType(leftType)
	rightPointerish := isPointerLikeType(rightType) || semantic.IsNullType(rightType)
	if !leftPointerish || !rightPointerish {
		return nil, nil, false, nil
	}
	operandType := s.binaryOperandType(expr.Op, leftType, rightType)
	left, _, err := s.emitExpr(expr.Left, operandType)
	if err != nil {
		return nil, nil, true, err
	}
	right, _, err := s.emitExpr(expr.Right, operandType)
	if err != nil {
		return nil, nil, true, err
	}
	pred := C.LLVMIntPredicate(C.LLVMIntEQ)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntNE)
	}
	cmp := C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("ptrcmptmp"))
	return cmp, resultType, true, nil
}
func (s *functionState) emitOptionalCompareExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr.Op != lexer.TOKEN_EQEQ && expr.Op != lexer.TOKEN_BANGEQ {
		return nil, nil, false, nil
	}
	var (
		optionalExpr ast.Expr
		optionalType *semantic.OptionalType
	)
	switch leftOptional := leftType.(type) {
	case *semantic.OptionalType:
		if semantic.IsNullType(rightType) {
			optionalExpr = expr.Left
			optionalType = leftOptional
		}
	}
	if optionalType == nil {
		if leftOptional, ok := leftType.(*semantic.OptionalType); ok {
			if _, isNull := expr.Right.(*ast.NullLit); isNull {
				optionalExpr = expr.Left
				optionalType = leftOptional
			}
		}
	}
	if optionalType == nil {
		if _, isNull := expr.Right.(*ast.NullLit); isNull {
			if leftOptional, ok := s.optionalCompareStoredType(expr.Left); ok {
				optionalExpr = expr.Left
				optionalType = leftOptional
			}
		}
	}
	if optionalType == nil {
		if rightOptional, ok := rightType.(*semantic.OptionalType); ok && semantic.IsNullType(leftType) {
			optionalExpr = expr.Right
			optionalType = rightOptional
		}
	}
	if optionalType == nil {
		if rightOptional, ok := rightType.(*semantic.OptionalType); ok {
			if _, isNull := expr.Left.(*ast.NullLit); isNull {
				optionalExpr = expr.Right
				optionalType = rightOptional
			}
		}
	}
	if optionalType == nil {
		if _, isNull := expr.Left.(*ast.NullLit); isNull {
			if rightOptional, ok := s.optionalCompareStoredType(expr.Right); ok {
				optionalExpr = expr.Right
				optionalType = rightOptional
			}
		}
	}
	if optionalType == nil {
		return nil, nil, false, nil
	}
	optionalValue, err := s.emitOptionalCompareOperandValue(optionalExpr, optionalType)
	if err != nil {
		return nil, nil, true, err
	}
	presentValue, err := s.extractOptionalPresent(optionalValue, optionalType)
	if err != nil {
		return nil, nil, true, err
	}
	if expr.Op == lexer.TOKEN_EQEQ {
		return C.LLVMBuildNot(s.builder, presentValue, cStringFree("optionalisnull")), resultType, true, nil
	}
	return presentValue, resultType, true, nil
}
func (s *functionState) optionalCompareStoredType(expr ast.Expr) (*semantic.OptionalType, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.optionalCompareStoredType(n.Inner)
	case *ast.Ident:
		if binding, ok := s.lookupBinding(n.Name); ok {
			if optionalType, ok := binding.typ.(*semantic.OptionalType); ok {
				return optionalType, true
			}
		}
	case *ast.FieldExpr:
		if ptr, fieldType, err := s.emitFieldAddress(n); err == nil && ptr != nil {
			if optionalType, ok := fieldType.(*semantic.OptionalType); ok {
				return optionalType, true
			}
		}
	}
	return nil, false
}
func (s *functionState) emitOptionalCompareOperandValue(expr ast.Expr, optionalType *semantic.OptionalType) (C.LLVMValueRef, error) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitOptionalCompareOperandValue(n.Inner, optionalType)
	case *ast.Ident:
		if binding, ok := s.lookupBinding(n.Name); ok {
			if storedOptional, ok := binding.typ.(*semantic.OptionalType); ok && semantic.SameType(storedOptional, optionalType) {
				return s.loadValue(binding.ptr, storedOptional, n.Name)
			}
		}
	case *ast.FieldExpr:
		ptr, fieldType, err := s.emitFieldAddress(n)
		if err == nil {
			if storedOptional, ok := fieldType.(*semantic.OptionalType); ok && semantic.SameType(storedOptional, optionalType) {
				return s.loadValue(ptr, storedOptional, n.Field)
			}
		}
	}
	optionalValue, _, err := s.emitExpr(expr, optionalType)
	return optionalValue, err
}
func (s *functionState) emitPointerArithmeticExpr(expr *ast.BinaryExpr, leftType semantic.Type, rightType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, bool, error) {
	leftRef, leftIsRef := leftType.(*semantic.RefType)
	rightRef, rightIsRef := rightType.(*semantic.RefType)
	leftIsNumeric := isNumericType(leftType)
	rightIsNumeric := isNumericType(rightType)

	var (
		baseExpr  ast.Expr
		baseType  *semantic.RefType
		indexExpr ast.Expr
	)

	switch {
	case leftIsRef && rightIsNumeric && (expr.Op == lexer.TOKEN_PLUS || expr.Op == lexer.TOKEN_MINUS):
		baseExpr, baseType, indexExpr = expr.Left, leftRef, expr.Right
	case expr.Op == lexer.TOKEN_PLUS && leftIsNumeric && rightIsRef:
		baseExpr, baseType, indexExpr = expr.Right, rightRef, expr.Left
	default:
		return nil, nil, false, nil
	}

	baseValue, _, err := s.emitExpr(baseExpr, baseType)
	if err != nil {
		return nil, nil, true, err
	}
	indexValue, _, err := s.emitExpr(indexExpr, nil)
	if err != nil {
		return nil, nil, true, err
	}
	if expr.Op == lexer.TOKEN_MINUS {
		indexValue = C.LLVMBuildNeg(s.builder, indexValue, cStringFree("ptridx.neg"))
	}
	elemLLVMType, err := s.g.lowerType(baseType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	indices := []C.LLVMValueRef{indexValue}
	ptr := C.LLVMBuildGEP2(s.builder, elemLLVMType, baseValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("ptrarith"))
	return ptr, resultType, true, nil
}
func (s *functionState) emitRuntimeStringCompareExpr(expr *ast.BinaryExpr, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (C.LLVMValueRef, semantic.Type, error) {
	firstExpr := expr.Left
	secondExpr := expr.Right
	if swap {
		firstExpr, secondExpr = secondExpr, firstExpr
	}
	if helperName == "ctx_streq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("cstrlit.eq.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
		if literalText, ok := s.staticCStringLiteral(firstExpr); ok {
			cmp, err := s.emitDStrStaticLiteralEqual(secondExpr, secondType, firstExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("cstrlit.eq.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
	}
	if helperName == "ctx_string_view_eq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitStringViewStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("strcmp.lit.not"))
			}
			return cmp, s.g.result.NamedTypes["bool"], nil
		}
	}
	if cmp, ok, err := s.emitSameExtentRuntimeStringCompareExpr(expr.Op, firstExpr, firstType, secondExpr, secondType); ok {
		if err != nil {
			return nil, nil, err
		}
		return cmp, s.g.result.NamedTypes["bool"], nil
	}
	if cmp, ok, err := s.emitDisjointRuntimeStringCompareExpr(expr.Op, firstExpr, firstType, secondExpr, secondType); ok {
		if err != nil {
			return nil, nil, err
		}
		return cmp, s.g.result.NamedTypes["bool"], nil
	}
	firstValue, _, err := s.emitExpr(firstExpr, firstType)
	if err != nil {
		return nil, nil, err
	}
	secondValue, _, err := s.emitExpr(secondExpr, secondType)
	if err != nil {
		return nil, nil, err
	}
	helperReturn := s.g.result.NamedTypes["int"]
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{firstType, secondType},
		Return: helperReturn,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{firstValue, secondValue}
	call := s.buildCall(llvmFnType, callee, args, "streqtmp")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, nil, err
	}
	zero := C.LLVMConstInt(helperLLVMType, 0, 0)
	pred := C.LLVMIntPredicate(C.LLVMIntNE)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = C.LLVMIntPredicate(C.LLVMIntEQ)
	}
	cmp := C.LLVMBuildICmp(s.builder, pred, call, zero, cStringFree("strcmp"))
	return cmp, s.g.result.NamedTypes["bool"], nil
}
func (s *functionState) emitSameExtentRuntimeStringCompareExpr(op lexer.TokenKind, firstExpr ast.Expr, firstType semantic.Type, secondExpr ast.Expr, secondType semantic.Type) (C.LLVMValueRef, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.ExprsHaveSameExtent(firstExpr, secondExpr) {
		return nil, false, nil
	}
	firstData, firstLen, firstLenType, firstKind, err := s.emitRuntimeStringCompareOperand(firstExpr, firstType)
	if err != nil {
		return nil, true, err
	}
	secondData, secondLen, secondLenType, secondKind, err := s.emitRuntimeStringCompareOperand(secondExpr, secondType)
	if err != nil {
		return nil, true, err
	}
	if firstKind == runtimeStringCompareNone || secondKind == runtimeStringCompareNone {
		return nil, false, nil
	}
	lenValue := firstLen
	lenType := firstLenType
	if lenValue == nil {
		lenValue = secondLen
		lenType = secondLenType
	}
	if lenValue == nil || lenType == nil {
		return nil, false, nil
	}
	usizeType := s.g.result.NamedTypes["usize"]
	coercedLen, err := s.coerceValue(lenValue, lenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	disjoint := s.g.result.ExprsAreDisjoint(firstExpr, secondExpr)
	cmp, err := s.emitMemcmpEqualValue(firstData, secondData, coercedLen, "streq.memcmp", disjoint)
	if err != nil {
		return nil, true, err
	}
	if op == lexer.TOKEN_BANGEQ {
		cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("streq.memcmp.not"))
	}
	return cmp, true, nil
}
func (s *functionState) emitDisjointRuntimeStringCompareExpr(op lexer.TokenKind, firstExpr ast.Expr, firstType semantic.Type, secondExpr ast.Expr, secondType semantic.Type) (C.LLVMValueRef, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.ExprsAreDisjoint(firstExpr, secondExpr) {
		return nil, false, nil
	}
	firstData, firstLen, firstLenType, firstKind, err := s.emitRuntimeStringCompareOperand(firstExpr, firstType)
	if err != nil {
		return nil, true, err
	}
	secondData, secondLen, secondLenType, secondKind, err := s.emitRuntimeStringCompareOperand(secondExpr, secondType)
	if err != nil {
		return nil, true, err
	}
	if firstKind == runtimeStringCompareNone || secondKind == runtimeStringCompareNone {
		return nil, false, nil
	}
	if firstLen == nil || firstLenType == nil || secondLen == nil || secondLenType == nil {
		return nil, false, nil
	}
	usizeType := s.g.result.NamedTypes["usize"]
	firstCoercedLen, err := s.coerceValue(firstLen, firstLenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	secondCoercedLen, err := s.coerceValue(secondLen, secondLenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	lenEqual := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), firstCoercedLen, secondCoercedLen, cStringFree("streq.disjoint.len.eq"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	memcmpBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("streq.disjoint.memcmp"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("streq.disjoint.merge"))
	C.LLVMBuildCondBr(s.builder, lenEqual, memcmpBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, memcmpBB)
	cmp, err := s.emitMemcmpEqualValue(firstData, secondData, firstCoercedLen, "streq.disjoint.memcmp", true)
	if err != nil {
		return nil, true, err
	}
	if op == lexer.TOKEN_BANGEQ {
		cmp = C.LLVMBuildNot(s.builder, cmp, cStringFree("streq.disjoint.memcmp.not"))
	}
	memcmpEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	phi := C.LLVMBuildPhi(s.builder, boolType, cStringFree("streq.disjoint.result"))
	fallbackRaw := C.ulonglong(0)
	if op == lexer.TOKEN_BANGEQ {
		fallbackRaw = 1
	}
	fallback := C.LLVMConstInt(boolType, fallbackRaw, 0)
	values := []C.LLVMValueRef{fallback, cmp}
	blocks := []C.LLVMBasicBlockRef{entryBlock, memcmpEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, true, nil
}
func (s *functionState) emitRuntimeStringCompareOperand(expr ast.Expr, exprType semantic.Type) (C.LLVMValueRef, C.LLVMValueRef, semantic.Type, runtimeStringCompareKind, error) {
	kind := classifyRuntimeStringCompareKind(exprType)
	if kind == runtimeStringCompareNone {
		return nil, nil, nil, kind, nil
	}
	value, _, err := s.emitExpr(expr, exprType)
	if err != nil {
		return nil, nil, nil, kind, err
	}
	switch kind {
	case runtimeStringCompareView:
		lenType := s.g.result.NamedTypes["i64"]
		data := C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("streq.view.data"))
		length := C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("streq.view.len"))
		return data, length, lenType, kind, nil
	case runtimeStringCompareDStr:
		lenType := s.g.result.NamedTypes["i64"]
		length, err := s.emitRuntimeStringLengthValue(value, exprType, lenType, "streq.len")
		if err != nil {
			return nil, nil, nil, kind, err
		}
		return value, length, lenType, kind, nil
	case runtimeStringCompareRaw:
		return value, nil, nil, kind, nil
	default:
		return nil, nil, nil, kind, nil
	}
}
func (s *functionState) emitRuntimeStringLengthValue(stringValue C.LLVMValueRef, stringType semantic.Type, resultType semantic.Type, name string) (C.LLVMValueRef, error) {
	helperType := &semantic.FuncType{
		Name:   "ctx_strlen",
		Params: []semantic.Type{stringType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared("ctx_strlen", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{stringValue}, name), nil
}
func (s *functionState) emitSpecializedRuntimeCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if value, actualType, handled, err := s.emitSpecializedStringViewLiteralCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSliceEqCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSlicesEqCall(expr); handled {
		return value, actualType, true, err
	}
	if value, actualType, handled, err := s.emitSpecializedRuntimeStringCompareCall(expr); handled {
		return value, actualType, true, err
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if ident.Name != "string_view_eq" && ident.Name != "ctx_string_view_eq" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[1])
	if !ok {
		return nil, nil, false, nil
	}
	firstType := s.exprType(expr.Args[0])
	if classifyRuntimeStringCompareKind(firstType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[0], firstType, expr.Args[1], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	intType := s.g.result.NamedTypes["int"]
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return C.LLVMBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}
func (s *functionState) staticIntLiteral(expr ast.Expr) (int64, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		value := n.Value
		if n.Suffix != "" {
			value += n.Suffix
		}
		return parseOptimizationExtentConstInt(value)
	case *ast.ParenExpr:
		return s.staticIntLiteral(n.Inner)
	case *ast.CastExpr:
		return s.staticIntLiteral(n.Operand)
	case *ast.CanExpr:
		return s.staticIntLiteral(n.Expr)
	default:
		return 0, false
	}
}
func (s *functionState) emitMinInt64Value(left C.LLVMValueRef, right C.LLVMValueRef, namePrefix string) C.LLVMValueRef {
	chooseLeft := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntSLE), left, right, cStringFree(namePrefix+".chooseleft"))
	leftBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".left"))
	rightBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".right"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".merge"))
	C.LLVMBuildCondBr(s.builder, chooseLeft, leftBB, rightBB)

	C.LLVMPositionBuilderAtEnd(s.builder, leftBB)
	leftEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, rightBB)
	rightEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, C.LLVMInt64TypeInContext(s.g.context), cStringFree(namePrefix))
	values := []C.LLVMValueRef{left, right}
	blocks := []C.LLVMBasicBlockRef{leftEnd, rightEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi
}
func (s *functionState) emitConstantClampedStringSliceOperand(expr ast.Expr, exprType semantic.Type, start int64, end int64, namePrefix string) (C.LLVMValueRef, C.LLVMValueRef, error) {
	if classifyRuntimeStringCompareKind(exprType) != runtimeStringCompareDStr {
		return nil, nil, fmt.Errorf("constant string slice specialization requires cstr operand")
	}
	stringValue, _, err := s.emitExpr(expr, exprType)
	if err != nil {
		return nil, nil, err
	}
	i64Type := s.g.result.NamedTypes["i64"]
	stringLen, err := s.emitRuntimeStringLengthValue(stringValue, exprType, i64Type, namePrefix+".len")
	if err != nil {
		return nil, nil, err
	}
	i64LLVMType := C.LLVMInt64TypeInContext(s.g.context)
	zeroI64 := C.LLVMConstInt(i64LLVMType, 0, 0)
	clampedStart := zeroI64
	if start > 0 {
		startValue := C.LLVMConstInt(i64LLVMType, C.ulonglong(start), 0)
		clampedStart = s.emitMinInt64Value(startValue, stringLen, namePrefix+".start")
	}
	clampedEnd := stringLen
	if end >= 0 {
		endValue := C.LLVMConstInt(i64LLVMType, C.ulonglong(end), 0)
		clampedEnd = s.emitMinInt64Value(endValue, stringLen, namePrefix+".end")
	}
	sliceLen := C.LLVMBuildSub(s.builder, clampedEnd, clampedStart, cStringFree(namePrefix+".slice.len"))
	sliceData := stringValue
	if start > 0 {
		usizeType := s.g.result.NamedTypes["usize"]
		clampedStartUsize, err := s.coerceValue(clampedStart, i64Type, usizeType)
		if err != nil {
			return nil, nil, err
		}
		i8LLVMType, err := s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, nil, err
		}
		indices := []C.LLVMValueRef{clampedStartUsize}
		sliceData = C.LLVMBuildGEP2(s.builder, i8LLVMType, stringValue, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(namePrefix+".data"))
	}
	return sliceData, sliceLen, nil
}
func (s *functionState) constantDStrSliceCall(expr ast.Expr) (ast.Expr, semantic.Type, int64, int64, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.constantDStrSliceCall(n.Inner)
	case *ast.CastExpr:
		return s.constantDStrSliceCall(n.Operand)
	case *ast.CanExpr:
		return s.constantDStrSliceCall(n.Expr)
	case *ast.CallExpr:
		ident, ok := n.Func.(*ast.Ident)
		if !ok || ident.Name != "ctx_string_slice" || len(n.Args) != 3 {
			return nil, nil, 0, 0, false
		}
		baseExpr := n.Args[0]
		baseType := s.exprType(baseExpr)
		if classifyRuntimeStringCompareKind(baseType) != runtimeStringCompareDStr {
			return nil, nil, 0, 0, false
		}
		start, ok := s.staticIntLiteral(n.Args[1])
		if !ok || start < 0 {
			return nil, nil, 0, 0, false
		}
		end, ok := s.staticIntLiteral(n.Args[2])
		if !ok || end < start {
			return nil, nil, 0, 0, false
		}
		return baseExpr, baseType, start, end, true
	default:
		return nil, nil, 0, 0, false
	}
}
