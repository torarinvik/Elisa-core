//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitStringCompareOperandValue(e ast.Expr, valueType semantic.Type) (llvmIRValueRef, error) {
	if ref, ok := s.exprType(e).(*semantic.RefType); ok {
		if _, ok := ref.Elem.(*semantic.SViewType); ok {
			ptr, _, err := s.emitExpr(e, s.exprType(e))
			if err != nil {
				return nil, err
			}
			return s.loadValue(ptr, ref.Elem, "strcmp.deref")
		}
	}
	value, _, err := s.emitExpr(e, valueType)
	return value, err
}

func (s *functionState) emitRuntimeStringCompareExpr(expr *ast.BinaryExpr, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (llvmIRValueRef, semantic.Type, error) {
	firstExpr := expr.Left
	secondExpr := expr.Right
	if swap {
		firstExpr, secondExpr = secondExpr, firstExpr
	}
	if helperName == "ctx_streq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitCStrStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = llvmBuildNot(s.builder, cmp, cStringFree("cstrlit.eq.not"))
			}
			return cmp, s.g.namedType("bool"), nil
		}
		if literalText, ok := s.staticCStringLiteral(firstExpr); ok {
			cmp, err := s.emitCStrStaticLiteralEqual(secondExpr, secondType, firstExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = llvmBuildNot(s.builder, cmp, cStringFree("cstrlit.eq.not"))
			}
			return cmp, s.g.namedType("bool"), nil
		}
	}
	if helperName == "ctx_string_view_eq" {
		if literalText, ok := s.staticCStringLiteral(secondExpr); ok {
			cmp, err := s.emitStringViewStaticLiteralEqual(firstExpr, firstType, secondExpr, literalText)
			if err != nil {
				return nil, nil, err
			}
			if expr.Op == lexer.TOKEN_BANGEQ {
				cmp = llvmBuildNot(s.builder, cmp, cStringFree("strcmp.lit.not"))
			}
			return cmp, s.g.namedType("bool"), nil
		}
	}
	if cmp, ok, err := s.emitSameExtentRuntimeStringCompareExpr(expr.Op, firstExpr, firstType, secondExpr, secondType); ok {
		if err != nil {
			return nil, nil, err
		}
		return cmp, s.g.namedType("bool"), nil
	}
	if cmp, ok, err := s.emitDisjointRuntimeStringCompareExpr(expr.Op, firstExpr, firstType, secondExpr, secondType); ok {
		if err != nil {
			return nil, nil, err
		}
		return cmp, s.g.namedType("bool"), nil
	}
	firstValue, err := s.emitStringCompareOperandValue(firstExpr, firstType)
	if err != nil {
		return nil, nil, err
	}
	secondValue, err := s.emitStringCompareOperandValue(secondExpr, secondType)
	if err != nil {
		return nil, nil, err
	}
	helperReturn := s.g.namedType("int")
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
	args := []llvmIRValueRef{firstValue, secondValue}
	call := s.buildCall(llvmFnType, callee, args, "streqtmp")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, nil, err
	}
	zero := llvmConstInt(helperLLVMType, 0, 0)
	pred := llvmIRIntPredicate(llvmIntNE)
	if expr.Op == lexer.TOKEN_BANGEQ {
		pred = llvmIRIntPredicate(llvmIntEQ)
	}
	cmp := llvmBuildICmp(s.builder, pred, call, zero, cStringFree("strcmp"))
	return cmp, s.g.namedType("bool"), nil
}
func (s *functionState) emitSameExtentRuntimeStringCompareExpr(op lexer.TokenKind, firstExpr ast.Expr, firstType semantic.Type, secondExpr ast.Expr, secondType semantic.Type) (llvmIRValueRef, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.semanticReadView().ExprsHaveSameExtent(firstExpr, secondExpr) {
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
	usizeType := s.g.namedType("usize")
	coercedLen, err := s.coerceValue(lenValue, lenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	disjoint := s.g.semanticReadView().ExprsAreDisjoint(firstExpr, secondExpr)
	cmp, err := s.emitMemcmpEqualValue(firstData, secondData, coercedLen, "streq.memcmp", disjoint)
	if err != nil {
		return nil, true, err
	}
	if op == lexer.TOKEN_BANGEQ {
		cmp = llvmBuildNot(s.builder, cmp, cStringFree("streq.memcmp.not"))
	}
	return cmp, true, nil
}
func (s *functionState) emitDisjointRuntimeStringCompareExpr(op lexer.TokenKind, firstExpr ast.Expr, firstType semantic.Type, secondExpr ast.Expr, secondType semantic.Type) (llvmIRValueRef, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.semanticReadView().ExprsAreDisjoint(firstExpr, secondExpr) {
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
	usizeType := s.g.namedType("usize")
	firstCoercedLen, err := s.coerceValue(firstLen, firstLenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	secondCoercedLen, err := s.coerceValue(secondLen, secondLenType, usizeType)
	if err != nil {
		return nil, true, err
	}
	lenEqual := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), firstCoercedLen, secondCoercedLen, cStringFree("streq.disjoint.len.eq"))
	entryBlock := llvmGetInsertBlock(s.builder)
	memcmpBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("streq.disjoint.memcmp"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("streq.disjoint.merge"))
	llvmBuildCondBr(s.builder, lenEqual, memcmpBB, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, memcmpBB)
	cmp, err := s.emitMemcmpEqualValue(firstData, secondData, firstCoercedLen, "streq.disjoint.memcmp", true)
	if err != nil {
		return nil, true, err
	}
	if op == lexer.TOKEN_BANGEQ {
		cmp = llvmBuildNot(s.builder, cmp, cStringFree("streq.disjoint.memcmp.not"))
	}
	memcmpEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := llvmInt1TypeInContext(s.g.context)
	phi := llvmBuildPhi(s.builder, boolType, cStringFree("streq.disjoint.result"))
	fallbackRaw := llvmIRUnsignedLongLong(0)
	if op == lexer.TOKEN_BANGEQ {
		fallbackRaw = 1
	}
	fallback := llvmConstInt(boolType, fallbackRaw, 0)
	values := []llvmIRValueRef{fallback, cmp}
	blocks := []llvmIRBlockRef{entryBlock, memcmpEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return phi, true, nil
}
func (s *functionState) emitRuntimeStringCompareOperand(expr ast.Expr, exprType semantic.Type) (llvmIRValueRef, llvmIRValueRef, semantic.Type, runtimeStringCompareKind, error) {
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
		lenType := s.g.namedType("i64")
		data := llvmBuildExtractValue(s.builder, value, 0, cStringFree("streq.view.data"))
		length := llvmBuildExtractValue(s.builder, value, 1, cStringFree("streq.view.len"))
		return data, length, lenType, kind, nil
	case runtimeStringCompareCStr:
		lenType := s.g.namedType("i64")
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
func (s *functionState) emitRuntimeStringLengthValue(stringValue llvmIRValueRef, stringType semantic.Type, resultType semantic.Type, name string) (llvmIRValueRef, error) {
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
	return s.buildCall(llvmFnType, callee, []llvmIRValueRef{stringValue}, name), nil
}
func (s *functionState) emitSpecializedRuntimeCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
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
	if !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
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
	intType := s.g.namedType("int")
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return llvmBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
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
func (s *functionState) emitMinInt64Value(left llvmIRValueRef, right llvmIRValueRef, namePrefix string) llvmIRValueRef {
	chooseLeft := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntSLE), left, right, cStringFree(namePrefix+".chooseleft"))
	leftBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".left"))
	rightBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".right"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(namePrefix+".merge"))
	llvmBuildCondBr(s.builder, chooseLeft, leftBB, rightBB)

	llvmPositionBuilderAtEnd(s.builder, leftBB)
	leftEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, rightBB)
	rightEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	phi := llvmBuildPhi(s.builder, llvmInt64TypeInContext(s.g.context), cStringFree(namePrefix))
	values := []llvmIRValueRef{left, right}
	blocks := []llvmIRBlockRef{leftEnd, rightEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return phi
}
func (s *functionState) emitConstantClampedStringSliceOperand(expr ast.Expr, exprType semantic.Type, start int64, end int64, namePrefix string) (llvmIRValueRef, llvmIRValueRef, error) {
	if classifyRuntimeStringCompareKind(exprType) != runtimeStringCompareCStr {
		return nil, nil, fmt.Errorf("constant string slice specialization requires cstr operand")
	}
	stringValue, _, err := s.emitExpr(expr, exprType)
	if err != nil {
		return nil, nil, err
	}
	i64Type := s.g.namedType("i64")
	stringLen, err := s.emitRuntimeStringLengthValue(stringValue, exprType, i64Type, namePrefix+".len")
	if err != nil {
		return nil, nil, err
	}
	i64LLVMType := llvmInt64TypeInContext(s.g.context)
	zeroI64 := llvmConstInt(i64LLVMType, 0, 0)
	clampedStart := zeroI64
	if start > 0 {
		startValue := llvmConstInt(i64LLVMType, llvmIRUnsignedLongLong(start), 0)
		clampedStart = s.emitMinInt64Value(startValue, stringLen, namePrefix+".start")
	}
	clampedEnd := stringLen
	if end >= 0 {
		endValue := llvmConstInt(i64LLVMType, llvmIRUnsignedLongLong(end), 0)
		clampedEnd = s.emitMinInt64Value(endValue, stringLen, namePrefix+".end")
	}
	sliceLen := llvmBuildSub(s.builder, clampedEnd, clampedStart, cStringFree(namePrefix+".slice.len"))
	sliceData := stringValue
	if start > 0 {
		usizeType := s.g.namedType("usize")
		clampedStartUsize, err := s.coerceValue(clampedStart, i64Type, usizeType)
		if err != nil {
			return nil, nil, err
		}
		i8LLVMType, err := s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, nil, err
		}
		indices := []llvmIRValueRef{clampedStartUsize}
		sliceData = llvmBuildGEP2(s.builder, i8LLVMType, stringValue, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree(namePrefix+".data"))
	}
	return sliceData, sliceLen, nil
}
func (s *functionState) constantCStrSliceCall(expr ast.Expr) (ast.Expr, semantic.Type, int64, int64, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.constantCStrSliceCall(n.Inner)
	case *ast.CastExpr:
		return s.constantCStrSliceCall(n.Operand)
	case *ast.CanExpr:
		return s.constantCStrSliceCall(n.Expr)
	case *ast.CallExpr:
		ident, ok := n.Func.(*ast.Ident)
		if !ok || ident.Name != "ctx_string_slice" || len(n.Args) != 3 {
			return nil, nil, 0, 0, false
		}
		if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(n, ident.Name) {
			return nil, nil, 0, 0, false
		}
		baseExpr := n.Args[0]
		baseType := s.exprType(baseExpr)
		if classifyRuntimeStringCompareKind(baseType) != runtimeStringCompareCStr {
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
