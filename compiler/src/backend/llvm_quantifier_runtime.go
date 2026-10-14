package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// emitQuantifierExpr lowers a range-bounded contract quantifier to a loop, so a debug contract check
// (an `ensure` or `invariant` at -O0) can evaluate it. The parser desugars `forall i in lo..<hi: P` to
// `forall i: (not (lo <= i and i < hi)) or P` and `exists i in lo..<hi: P` to
// `exists i: (lo <= i and i < hi) and P`; exactly those single-binder shapes are lowered:
//
//	result = forall; for i in lo..<hi: if P != forall: result = !forall; break
//
// Any other quantifier is unbounded and has no runtime meaning.
func (s *functionState) emitQuantifierExpr(n *ast.QuantifierExpr) (C.LLVMValueRef, error) {
	lo, hi, binder, body, ok := rangeQuantifierParts(n)
	if !ok {
		return nil, fmt.Errorf("unsupported expression %T (an unbounded quantifier has no runtime check)", n)
	}
	binderType := s.exprType(binder)
	if binderType == nil {
		return nil, fmt.Errorf("quantifier binder %s has no type", binder.Name)
	}
	llvmBinder, err := s.g.lowerType(binderType)
	if err != nil {
		return nil, err
	}
	loVal, loType, err := s.emitExpr(lo, binderType)
	if err != nil {
		return nil, err
	}
	if loVal, err = s.coerceValue(loVal, loType, binderType); err != nil {
		return nil, err
	}
	hiVal, hiType, err := s.emitExpr(hi, binderType)
	if err != nil {
		return nil, err
	}
	if hiVal, err = s.coerceValue(hiVal, hiType, binderType); err != nil {
		return nil, err
	}
	boolType := C.LLVMInt1TypeInContext(s.g.context)
	forall := !n.Exists
	initial := C.LLVMConstInt(boolType, 0, 0)
	if forall {
		initial = C.LLVMConstInt(boolType, 1, 0)
	}
	resultPtr, err := s.createEntryAllocaRaw("quant.result", s.exprType(n))
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, initial, resultPtr)
	indexPtr, err := s.createEntryAllocaRaw("quant.index", binderType)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, loVal, indexPtr)

	headBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("quant.head"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("quant.body"))
	stepBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("quant.step"))
	foundBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("quant.found"))
	doneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("quant.done"))
	C.LLVMBuildBr(s.builder, headBB)

	C.LLVMPositionBuilderAtEnd(s.builder, headBB)
	index := C.LLVMBuildLoad2(s.builder, llvmBinder, indexPtr, cStringFree("quant.i"))
	pred := C.LLVMIntPredicate(C.LLVMIntULT)
	if isSignedIntegerType(binderType) {
		pred = C.LLVMIntPredicate(C.LLVMIntSLT)
	}
	inRange := C.LLVMBuildICmp(s.builder, pred, index, hiVal, cStringFree("quant.inrange"))
	C.LLVMBuildCondBr(s.builder, inRange, bodyBB, doneBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	s.defineBinding(binder.Name, valueBinding{ptr: indexPtr, typ: binderType})
	bodyVal, _, err := s.emitExpr(body, nil)
	s.popScope()
	if err != nil {
		return nil, err
	}
	// forall: a false body decides (result false); exists: a true body decides (result true).
	if forall {
		C.LLVMBuildCondBr(s.builder, bodyVal, stepBB, foundBB)
	} else {
		C.LLVMBuildCondBr(s.builder, bodyVal, foundBB, stepBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, stepBB)
	next := C.LLVMBuildAdd(s.builder, C.LLVMBuildLoad2(s.builder, llvmBinder, indexPtr, cStringFree("quant.cur")), C.LLVMConstInt(llvmBinder, 1, 0), cStringFree("quant.next"))
	C.LLVMBuildStore(s.builder, next, indexPtr)
	C.LLVMBuildBr(s.builder, headBB)

	C.LLVMPositionBuilderAtEnd(s.builder, foundBB)
	C.LLVMBuildStore(s.builder, C.LLVMConstNot(initial), resultPtr)
	C.LLVMBuildBr(s.builder, doneBB)

	C.LLVMPositionBuilderAtEnd(s.builder, doneBB)
	return C.LLVMBuildLoad2(s.builder, boolType, resultPtr, cStringFree("quant.value")), nil
}

// rangeQuantifierParts recognizes the parser's range-binder desugaring (see emitQuantifierExpr).
func rangeQuantifierParts(n *ast.QuantifierExpr) (lo, hi ast.Expr, binder *ast.Ident, body ast.Expr, ok bool) {
	if n == nil || len(n.Vars) != 1 || n.In != nil {
		return nil, nil, nil, nil, false
	}
	outer, isBin := n.Body.(*ast.BinaryExpr)
	if !isBin || outer == nil {
		return nil, nil, nil, nil, false
	}
	var guard ast.Expr
	switch {
	case !n.Exists && outer.Op == lexer.TOKEN_OR:
		not, isNot := outer.Left.(*ast.UnaryExpr)
		if !isNot || not == nil || not.Op != lexer.TOKEN_NOT {
			return nil, nil, nil, nil, false
		}
		guard = not.Operand
	case n.Exists && outer.Op == lexer.TOKEN_AND:
		guard = outer.Left
	default:
		return nil, nil, nil, nil, false
	}
	g, isBin := guard.(*ast.BinaryExpr)
	if !isBin || g == nil || g.Op != lexer.TOKEN_AND {
		return nil, nil, nil, nil, false
	}
	low, isLow := g.Left.(*ast.BinaryExpr)
	high, isHigh := g.Right.(*ast.BinaryExpr)
	if !isLow || !isHigh || low == nil || high == nil || low.Op != lexer.TOKEN_LTEQ || high.Op != lexer.TOKEN_LT {
		return nil, nil, nil, nil, false
	}
	lowVar, ok1 := low.Right.(*ast.Ident)
	highVar, ok2 := high.Left.(*ast.Ident)
	if !ok1 || !ok2 || lowVar == nil || highVar == nil || lowVar.Name != n.Vars[0] || highVar.Name != n.Vars[0] {
		return nil, nil, nil, nil, false
	}
	return low.Left, high.Right, highVar, outer.Right, true
}
