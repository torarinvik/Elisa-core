//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) listLiteralTargetArrayType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.ArrayType, bool, error) {
	if expectedArray, ok := expected.(*semantic.ArrayType); ok {
		return expectedArray, true, nil
	}
	actualArray, ok := s.exprType(expr).(*semantic.ArrayType)
	if !ok {
		return nil, false, nil
	}
	return actualArray, true, nil
}

// dictLiteralTargetType resolves the dict type an empty `{}` literal should lower to, preferring
// the expected (target) type and falling back to the literal's own resolved type. Mirrors
// listLiteralTargetDArrayType for the dict analogue of `[]`.
func (s *functionState) dictLiteralTargetType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.DictType, bool) {
	if dictType, ok := semantic.StripAggregateStateType(expected).(*semantic.DictType); ok && dictType != nil {
		return dictType, true
	}
	if dictType, ok := semantic.StripAggregateStateType(s.exprType(expr)).(*semantic.DictType); ok && dictType != nil {
		return dictType, true
	}
	return nil, false
}

func (s *functionState) setLiteralTargetType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.SetType, bool) {
	if setType, ok := semantic.StripAggregateStateType(expected).(*semantic.SetType); ok && setType != nil {
		return setType, true
	}
	if setType, ok := semantic.StripAggregateStateType(s.exprType(expr)).(*semantic.SetType); ok && setType != nil {
		return setType, true
	}
	return nil, false
}

func (s *functionState) listLiteralTargetDArrayType(expr *ast.ListLitExpr, expected semantic.Type) (*semantic.DArrayType, error) {
	if expectedDArray, ok := expected.(*semantic.DArrayType); ok {
		return expectedDArray, nil
	}
	actualDArray, ok := s.exprType(expr).(*semantic.DArrayType)
	if !ok {
		return nil, fmt.Errorf("list literal did not resolve to a fixed array or darray type")
	}
	return actualDArray, nil
}
func (s *functionState) emitStackTempZeroed(t semantic.Type, name string) (C.LLVMValueRef, error) {
	zero, err := s.zeroValue(t)
	if err != nil {
		return nil, err
	}
	return s.emitStackTempValue(zero, t, name)
}
func (s *functionState) emitStackTempValue(value C.LLVMValueRef, t semantic.Type, name string) (C.LLVMValueRef, error) {
	alloca, err := s.createTempStorage(name, t)
	if err != nil {
		return nil, err
	}
	// Route through storeValue so large `zeroed` aggregates lower to llvm.memset
	// (and large aggregate copies to memcpy) rather than a first-class
	// `store <bigtype> zeroinitializer`, which llc -O0 expands into a giant
	// per-element SelectionDAG (e.g. a 1MB struct took ~220s to compile).
	if err := s.storeValue(alloca, value, t, name); err != nil {
		return nil, err
	}
	return alloca, nil
}
func (s *functionState) emitBoolLiteral(expr *ast.BoolLit) (C.LLVMValueRef, semantic.Type, error) {
	llvmType, err := s.g.lowerBuiltin("bool")
	if err != nil {
		return nil, nil, err
	}
	var raw C.ulonglong
	if expr.Value {
		raw = 1
	}
	return C.LLVMConstInt(llvmType, raw, 0), s.g.namedType("bool"), nil
}
func (s *functionState) emitNullLiteral() (C.LLVMValueRef, semantic.Type, error) {
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	return C.LLVMConstNull(ptrType), &semantic.NullType{}, nil
}
func (s *functionState) emitBinaryExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.LoweredCall != nil {
		return s.emitExpr(expr.LoweredCall, nil)
	}
	if expr.Op == lexer.TOKEN_IS {
		return s.emitIsExpr(expr)
	}
	if expr.Op == lexer.TOKEN_IN {
		return s.emitMembershipExpr(expr)
	}
	if expr.Op == lexer.TOKEN_AND || expr.Op == lexer.TOKEN_OR {
		return s.emitLogicalExpr(expr)
	}
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(s.exprType(expr.Left), s.exprType(expr.Right)); ok && (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) {
		return s.emitRuntimeStringCompareExpr(expr, helperName, firstType, secondType, swap)
	}
	leftType := s.exprType(expr.Left)
	rightType := s.exprType(expr.Right)
	resultType := s.exprType(expr)
	if value, actualType, handled, err := s.emitOptionalCompareExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerCompareExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerArithmeticExpr(expr, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) &&
		semantic.IsAggregateValueEqualityUnsupported(leftType) && semantic.IsAggregateValueEqualityUnsupported(rightType) {
		// Semantic analysis rejects this shape first. Keep the backend total as
		// well: a missed/externally-built AST must never reach LLVMBuildICmp with
		// an aggregate operand and leave an invalid module behind.
		return nil, nil, fmt.Errorf("aggregate values do not support %s", lexer.TokenName(expr.Op))
	}
	operandType := s.binaryOperandType(expr.Op, leftType, rightType)
	// Auto-deref a reference operand to its numeric/bool pointee for `==`/`!=`.
	// binaryOperandType leaves a ref as-is for equality (so the genuine pointer/null
	// comparison in emitPointerCompareExpr keeps pointer operands); but that path has
	// already bailed by now, so any remaining ref-vs-value equality must compare the
	// pointee. Without this, `someRef != 300` (e.g. a `T&` returned inline from a call)
	// would emit the operand as a raw pointer and the icmp would compare the address.
	if expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ {
		operandType = backendValueContextOperandType(operandType)
	}

	left, _, err := s.emitExpr(expr.Left, operandType)
	if err != nil {
		return nil, nil, err
	}
	right, _, err := s.emitExpr(expr.Right, operandType)
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := operandType.(*semantic.EnumType); ok && (expr.Op == lexer.TOKEN_EQEQ || expr.Op == lexer.TOKEN_BANGEQ) {
		return s.emitEnumCompareExpr(expr.Op, enumType, left, right, resultType)
	}

	// In contracts-live builds (debug / -O0 / forced contracts), signed `+`/`-`/`*` TRAP on
	// two's-complement overflow. This backs the static verifier's assumption that signed arithmetic
	// does not overflow (chosen semantics: trap in debug, wrap in release), so a statically-discharged
	// `ensure result > x` for `return x + k` can never be silently violated where contracts are
	// enforced. Release builds keep the plain wrapping op for performance.
	if (s.g.optLevel == OptimizationLevel0 || s.g.forceContracts) && isSignedIntegerType(operandType) &&
		(expr.Op == lexer.TOKEN_PLUS || expr.Op == lexer.TOKEN_MINUS || expr.Op == lexer.TOKEN_STAR) {
		llvmOperandType, err := s.g.lowerType(operandType)
		if err != nil {
			return nil, nil, err
		}
		value, err := s.emitCheckedSignedBinary(expr.Op, left, right, llvmOperandType, "signed.arith")
		if err != nil {
			return nil, nil, err
		}
		return value, resultType, nil
	}

	switch expr.Op {
	case lexer.TOKEN_PLUS:
		if isFloatType(operandType) {
			return s.fpContract(C.LLVMBuildFAdd(s.builder, left, right, cStringFree("addtmp"))), resultType, nil
		}
		return C.LLVMBuildAdd(s.builder, left, right, cStringFree("addtmp")), resultType, nil
	case lexer.TOKEN_MINUS:
		if isFloatType(operandType) {
			return s.fpContract(C.LLVMBuildFSub(s.builder, left, right, cStringFree("subtmp"))), resultType, nil
		}
		return C.LLVMBuildSub(s.builder, left, right, cStringFree("subtmp")), resultType, nil
	case lexer.TOKEN_STAR:
		if isFloatType(operandType) {
			return s.fpContract(C.LLVMBuildFMul(s.builder, left, right, cStringFree("multmp"))), resultType, nil
		}
		return C.LLVMBuildMul(s.builder, left, right, cStringFree("multmp")), resultType, nil
	case lexer.TOKEN_SLASH:
		if isFloatType(operandType) {
			return s.fpContractReciprocal(C.LLVMBuildFDiv(s.builder, left, right, cStringFree("divtmp"))), resultType, nil
		}
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildSDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
		}
		return C.LLVMBuildUDiv(s.builder, left, right, cStringFree("divtmp")), resultType, nil
	case lexer.TOKEN_PERCENT:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildSRem(s.builder, left, right, cStringFree("remtmp")), resultType, nil
		}
		return C.LLVMBuildURem(s.builder, left, right, cStringFree("remtmp")), resultType, nil
	case lexer.TOKEN_PIPE:
		return C.LLVMBuildOr(s.builder, left, right, cStringFree("ortmp")), resultType, nil
	case lexer.TOKEN_CARET:
		return C.LLVMBuildXor(s.builder, left, right, cStringFree("xortmp")), resultType, nil
	case lexer.TOKEN_AMPERSAND:
		return C.LLVMBuildAnd(s.builder, left, right, cStringFree("andtmp")), resultType, nil
	case lexer.TOKEN_LSHIFT:
		return C.LLVMBuildShl(s.builder, left, right, cStringFree("shltmp")), resultType, nil
	case lexer.TOKEN_RSHIFT:
		if isSignedIntegerType(operandType) {
			return C.LLVMBuildAShr(s.builder, left, right, cStringFree("shrtmp")), resultType, nil
		}
		return C.LLVMBuildLShr(s.builder, left, right, cStringFree("shrtmp")), resultType, nil
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if isFloatType(operandType) {
			pred, err := llvmFloatPredicate(expr.Op)
			if err != nil {
				return nil, nil, err
			}
			return C.LLVMBuildFCmp(s.builder, pred, left, right, cStringFree("cmptmp")), resultType, nil
		}
		pred, err := llvmIntPredicate(expr.Op, operandType)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMBuildICmp(s.builder, pred, left, right, cStringFree("cmptmp")), resultType, nil
	default:
		return nil, nil, fmt.Errorf("unsupported binary operator %s", lexer.TokenName(expr.Op))
	}
}
func (s *functionState) emitMembershipExpr(expr *ast.BinaryExpr) (C.LLVMValueRef, semantic.Type, error) {
	if setType, _, ok := builtinSetReceiverType(s.exprType(expr.Right)); ok && setType != nil {
		setValue, setType, err := s.emitBuiltinSetReceiverValue(expr.Right, s.exprType(expr.Right))
		if err != nil {
			return nil, nil, err
		}
		leftValue, _, err := s.emitExpr(expr.Left, setType.Elem)
		if err != nil {
			return nil, nil, err
		}
		callee, helperType, err := s.ensureRuntimeFunction("arena_set_contains", map[string]semantic.Type{"T": setType.Elem})
		if err != nil {
			return nil, nil, err
		}
		llvmType, err := s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, err
		}
		value := s.buildCall(llvmType, callee, []C.LLVMValueRef{setValue, leftValue}, "set.in")
		return value, s.g.namedType("bool"), nil
	}
	list, ok := s.membershipCandidateList(expr.Right)
	if !ok || list == nil {
		return nil, nil, fmt.Errorf("membership operator requires a list literal or tokenset on the right-hand side")
	}
	resultType := s.g.namedType("bool")
	boolLLVMType := C.LLVMInt1TypeInContext(s.g.context)
	if len(list.Elems) == 0 {
		return C.LLVMConstInt(boolLLVMType, 0, 0), resultType, nil
	}
	leftType := s.exprType(expr.Left)
	leftValue, _, err := s.emitExpr(expr.Left, leftType)
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("membership.merge"))
	incomingValues := make([]C.LLVMValueRef, 0, len(list.Elems))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(list.Elems))
	for i, elem := range list.Elems {
		currentBlock := C.LLVMGetInsertBlock(s.builder)
		cmp, err := s.emitMembershipCompareValueAndExpr(leftValue, leftType, elem)
		if err != nil {
			return nil, nil, err
		}
		if i == len(list.Elems)-1 {
			C.LLVMBuildBr(s.builder, mergeBB)
			incomingValues = append(incomingValues, cmp)
			incomingBlocks = append(incomingBlocks, currentBlock)
			break
		}
		nextBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(fmt.Sprintf("membership.next.%d", i)))
		C.LLVMBuildCondBr(s.builder, cmp, mergeBB, nextBB)
		incomingValues = append(incomingValues, C.LLVMConstInt(boolLLVMType, 1, 0))
		incomingBlocks = append(incomingBlocks, currentBlock)
		C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, boolLLVMType, cStringFree("membership.result"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) membershipCandidateList(expr ast.Expr) (*ast.ListLitExpr, bool) {
	if list, ok := expr.(*ast.ListLitExpr); ok {
		return list, true
	}
	if rangeExpr, ok := expr.(*ast.MembershipRangeExpr); ok {
		return &ast.ListLitExpr{Position: rangeExpr.Pos(), Elems: []ast.Expr{rangeExpr}}, true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil || s == nil || s.g == nil || s.g.result == nil || !s.g.hasGlobalScope() {
		return nil, false
	}
	sym, ok := s.g.lookupGlobal(ident.Name)
	if !ok || sym == nil {
		return nil, false
	}
	switch decl := sym.Node.(type) {
	case *ast.TokenSetDecl:
		if decl == nil {
			return nil, false
		}
		return decl.Value, true
	case *ast.CharsetDecl:
		return s.backendCharsetMembershipList(decl), true
	default:
		return nil, false
	}
}

func (s *functionState) backendCharsetMembershipList(decl *ast.CharsetDecl) *ast.ListLitExpr {
	if decl == nil {
		return nil
	}
	elems := s.backendCharsetMembershipElems(decl.Terms, map[*ast.CharsetDecl]bool{decl: true})
	return &ast.ListLitExpr{Position: decl.Position, Elems: elems}
}

func (s *functionState) backendCharsetMembershipElems(terms []ast.LexerCharClassTerm, visiting map[*ast.CharsetDecl]bool) []ast.Expr {
	elems := make([]ast.Expr, 0, len(terms))
	for _, term := range terms {
		if term.Ref {
			ref := s.backendCharsetRef(term.Name)
			if ref == nil || visiting[ref] {
				continue
			}
			visiting[ref] = true
			elems = append(elems, s.backendCharsetMembershipElems(ref.Terms, visiting)...)
			delete(visiting, ref)
			continue
		}
		start := &ast.CharLit{Position: term.Position, Value: term.Start}
		if term.Range {
			elems = append(elems, &ast.MembershipRangeExpr{Position: term.Position, Start: start, End: &ast.CharLit{Position: term.Position, Value: term.End}, Op: lexer.TOKEN_RANGE})
			continue
		}
		elems = append(elems, start)
	}
	return elems
}

func (s *functionState) backendCharsetRef(name string) *ast.CharsetDecl {
	if s == nil || s.g == nil || s.g.result == nil || !s.g.hasGlobalScope() {
		return nil
	}
	sym, ok := s.g.lookupGlobal(name)
	if !ok || sym == nil {
		return nil
	}
	ref, _ := sym.Node.(*ast.CharsetDecl)
	return ref
}
func (s *functionState) emitMembershipCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rightExpr ast.Expr) (C.LLVMValueRef, error) {
	if rangeExpr, ok := rightExpr.(*ast.MembershipRangeExpr); ok {
		return s.emitMembershipRangeCompareValueAndExpr(leftValue, leftType, rangeExpr)
	}
	rightType := s.exprType(rightExpr)
	resultType := s.g.namedType("bool")
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(leftType, rightType); ok {
		return s.emitRuntimeStringCompareValues(lexer.TOKEN_EQEQ, leftValue, leftType, rightExpr, helperName, firstType, secondType, swap)
	}
	if value, handled, err := s.emitMembershipOptionalCompareValueAndExpr(leftValue, leftType, rightExpr, rightType, resultType); handled {
		return value, err
	}
	if value, handled, err := s.emitMembershipPointerCompareValueAndExpr(leftValue, leftType, rightExpr, rightType, resultType); handled {
		return value, err
	}
	operandType := s.binaryOperandType(lexer.TOKEN_EQEQ, leftType, rightType)
	coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
	if err != nil {
		return nil, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, operandType)
	if err != nil {
		return nil, err
	}
	if enumType, ok := operandType.(*semantic.EnumType); ok {
		cmp, _, err := s.emitEnumCompareExpr(lexer.TOKEN_EQEQ, enumType, coercedLeft, rightValue, resultType)
		return cmp, err
	}
	if isFloatType(operandType) {
		return C.LLVMBuildFCmp(s.builder, C.LLVMRealOEQ, coercedLeft, rightValue, cStringFree("membership.eq")), nil
	}
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), coercedLeft, rightValue, cStringFree("membership.eq")), nil
}

func (s *functionState) emitMembershipRangeCompareValueAndExpr(leftValue C.LLVMValueRef, leftType semantic.Type, rangeExpr *ast.MembershipRangeExpr) (C.LLVMValueRef, error) {
	if rangeExpr == nil {
		return nil, fmt.Errorf("membership range expression is nil")
	}
	startType := s.exprType(rangeExpr.Start)
	endType := s.exprType(rangeExpr.End)
	operandType := s.binaryOperandType(lexer.TOKEN_LTEQ, leftType, startType)
	operandType = s.binaryOperandType(lexer.TOKEN_LTEQ, operandType, endType)
	coercedLeft, err := s.coerceValue(leftValue, leftType, operandType)
	if err != nil {
		return nil, err
	}
	startValue, _, err := s.emitExpr(rangeExpr.Start, operandType)
	if err != nil {
		return nil, err
	}
	endValue, _, err := s.emitExpr(rangeExpr.End, operandType)
	if err != nil {
		return nil, err
	}
	lowerPred, err := llvmIntPredicate(lexer.TOKEN_GTEQ, operandType)
	if err != nil {
		return nil, err
	}
	upperOp := lexer.TOKEN_LTEQ
	if rangeExpr.Op == lexer.TOKEN_RANGE_LT {
		upperOp = lexer.TOKEN_LT
	}
	upperPred, err := llvmIntPredicate(upperOp, operandType)
	if err != nil {
		return nil, err
	}
	lower := C.LLVMBuildICmp(s.builder, lowerPred, coercedLeft, startValue, cStringFree("membership.range.lower"))
	upper := C.LLVMBuildICmp(s.builder, upperPred, coercedLeft, endValue, cStringFree("membership.range.upper"))
	return C.LLVMBuildAnd(s.builder, lower, upper, cStringFree("membership.range")), nil
}

// fpAllowContract marks a floating-point instruction as contraction-allowed so the backend may fuse
// multiply-add into fma (clang's default). Returns the same value for convenient inline use.
