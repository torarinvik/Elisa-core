//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitCastExpr(expr *ast.CastExpr) (llvmIRValueRef, semantic.Type, error) {
	// A postfix-shorthand cast `recv.Name()` whose target was not a type is lowered
	// by the analyzer to the call `Name(recv)`; emit that call instead.
	if s != nil && s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnPostfixShorthandCallsMap() {
		if call, ok := s.g.semanticReadView().LookupPostfixShorthandCalls(expr); ok && call != nil {
			return s.emitExpr(call, s.exprType(expr))
		}
	}
	if s != nil && s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnCastHooksMap() {
		if sym, ok := s.g.semanticReadView().LookupCastHooks(expr); ok && sym != nil {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok || fnType == nil || len(fnType.Params) == 0 {
				return nil, nil, fmt.Errorf("invalid semantic cast hook for %T", expr)
			}
			args, err := s.emitCastHookArgs(fmt.Sprintf("%T", expr), expr.Operand, fnType)
			if err != nil {
				return nil, nil, err
			}
			callee, err := s.g.ensureFunctionDeclared(sym.Name, fnType)
			if err != nil {
				return nil, nil, err
			}
			llvmFnType, err := s.g.lowerFunctionType(fnType)
			if err != nil {
				return nil, nil, err
			}
			call := s.buildCall(llvmFnType, callee, args, "casthook")
			return call, fnType.Return, nil
		}
	}
	targetType := s.exprType(expr)
	if targetType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for cast target")
	}
	operandExpected := semantic.Type(nil)
	if _, ok := expr.Operand.(*ast.ZeroedLit); ok {
		operandExpected = targetType
	}
	var (
		value      llvmIRValueRef
		actualType semantic.Type
		err        error
	)
	if addr := castOperandAddrOf(expr.Operand); addr != nil {
		// `(&r).cast[T&]` is the reborrow idiom: it reinterprets the place r, and
		// for a reference binding that is its referent, not r's slot -- although
		// the analyzer types the `&r` as `T&&`. The loader's `(&self).cast[mutable
		// T&]` depends on it, and so does every desugar that wraps `&x` in a cast
		// (`wait all g` is `task_group_wait_all((&g).cast[mutable TaskGroup&])`).
		value, actualType, err = s.emitPlaceAddrOfExpr(addr)
	} else {
		value, actualType, err = s.emitExpr(expr.Operand, operandExpected)
	}
	if err != nil {
		return nil, nil, err
	}
	// An explicit `.cast[<integer>]` on a pointer/reference reinterprets the pointer
	// ADDRESS (ptrtoint); it never dereferences the pointee. Without this the
	// ref-to-numeric operand falls into coerceValue's implicit auto-deref -- correct
	// for the *value-conversion* forms (`off.u64()` reads a `usize&`'s pointee) but
	// wrong for an explicit reinterpret cast: `name.cast[usize]` would read the first
	// byte at the address instead of the address itself. Gated to the explicit
	// `.cast[T]` origin so the `.u64()`/`as`/`to` value forms keep their deref; makes
	// every integer target behave like the established `.uintptr()` idiom.
	if value != nil && expr.Origin == ast.CastExprOriginExplicitCast &&
		isReferenceLikeOperand(actualType) && isIntegerCastTarget(targetType) &&
		llvmGetTypeKind(llvmTypeOf(value)) == llvmPointerTypeKind {
		expectedLLVM, err := s.g.lowerType(targetType)
		if err != nil {
			return nil, nil, err
		}
		return llvmBuildPtrToInt(s.builder, value, expectedLLVM, cStringFree("cast.addr")), targetType, nil
	}
	coerced, err := s.coerceValue(value, actualType, targetType)
	if err != nil {
		return nil, nil, err
	}
	return coerced, targetType, nil
}

// isIntegerCastTarget reports whether t is an integer reinterpret target (numeric
// but not floating point) for an explicit pointer/reference `.cast`.
func isIntegerCastTarget(t semantic.Type) bool {
	return isNumericCastType(t) && !isFloatType(t)
}

// isReferenceLikeOperand reports whether t is a pointer/reference whose address an
// explicit integer `.cast` should reinterpret (ptrtoint) rather than dereference.
func isReferenceLikeOperand(t semantic.Type) bool {
	switch v := t.(type) {
	case *semantic.RefType:
		return true
	case *semantic.OptionalType:
		return isReferenceLikeOperand(v.Value)
	}
	return isPointerLikeType(t)
}
func (s *functionState) emitSizeofExpr(expr *ast.SizeofExpr) (llvmIRValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	size, err := s.sizeOfType(t)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return llvmConstInt(usizeType, llvmIRUnsignedLongLong(size), 0), s.g.namedType("usize"), nil
}

func (s *functionState) emitAlignofExpr(expr *ast.AlignofExpr) (llvmIRValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	alignment, err := s.g.abiAlignmentOfType(t)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return llvmConstInt(usizeType, llvmIRUnsignedLongLong(alignment), 0), s.g.namedType("usize"), nil
}

func (s *functionState) emitOffsetofExpr(expr *ast.OffsetofExpr) (llvmIRValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	_, fieldIndex, containerType, _, err := s.g.fieldInfo(t, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, nil, err
	}
	offset, err := s.g.abiOffsetOfLLVMElement(containerLLVMType, fieldIndex)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return llvmConstInt(usizeType, llvmIRUnsignedLongLong(offset), 0), s.g.namedType("usize"), nil
}

// castOperandAddrOf returns the `&x` a cast operand is, peeling parentheses.
func castOperandAddrOf(operand ast.Expr) *ast.AddrOfExpr {
	for {
		paren, ok := operand.(*ast.ParenExpr)
		if !ok || paren == nil {
			break
		}
		operand = paren.Inner
	}
	addr, _ := operand.(*ast.AddrOfExpr)
	return addr
}
