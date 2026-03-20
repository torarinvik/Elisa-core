//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"strconv"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

func (s *functionState) sizeOfType(t semantic.Type) (uint64, error) {
	return s.g.abiSizeOfType(t)
}

func shapeFromTypeExpr(expr ast.TypeExpr) semantic.Shape {
	if named, ok := expr.(*ast.NamedType); ok {
		return &semantic.NamedShape{Name: named.Name}
	}
	return &semantic.NamedShape{Name: "?"}
}

func shapeFromValueExpr(expr ast.Expr) semantic.Shape {
	switch n := expr.(type) {
	case *ast.Ident:
		return &semantic.NamedShape{Name: n.Name}
	case *ast.IntLit:
		return &semantic.NamedShape{Name: n.Value}
	default:
		return &semantic.NamedShape{Name: "?"}
	}
}

func normalizeIf(stmt *ast.IfStmt) *ast.IfStmt {
	if stmt == nil || len(stmt.Elifs) == 0 {
		return stmt
	}
	elseBody := stmt.Else
	for i := len(stmt.Elifs) - 1; i >= 0; i-- {
		elif := stmt.Elifs[i]
		elseBody = []ast.Stmt{&ast.IfStmt{Position: elif.Position, Cond: elif.Cond, Then: elif.Body, Else: elseBody}}
	}
	return &ast.IfStmt{Position: stmt.Position, Cond: stmt.Cond, Then: stmt.Then, Else: elseBody}
}

func (s *functionState) evalConstIntExpr(expr ast.Expr) (int64, error) {
	value, ok := s.evalConstExpr(expr)
	if !ok || value.Kind != semantic.ConstInt {
		return 0, fmt.Errorf("expression is not a compile-time integer constant")
	}
	return value.Int, nil
}

func (s *functionState) evalConstBoolExpr(expr ast.Expr) (bool, bool) {
	value, ok := evalConstExprWithLookup(expr, s.g.constValue)
	if !ok || value.Kind != semantic.ConstBool {
		return false, false
	}
	return value.Bool, true
}

func (s *functionState) evalConstExpr(expr ast.Expr) (semantic.ConstValue, bool) {
	return evalConstExprWithLookup(expr, s.g.constValue)
}

func (g *llvmGenerator) evalConstExpr(expr ast.Expr) (semantic.ConstValue, bool) {
	return evalConstExprWithLookup(expr, g.constValue)
}

func evalConstExprWithLookup(expr ast.Expr, lookup func(string) (semantic.ConstValue, bool)) (semantic.ConstValue, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		value, err := strconv.ParseInt(n.Value, 0, 64)
		if err != nil {
			return semantic.ConstValue{}, false
		}
		return semantic.ConstValue{Kind: semantic.ConstInt, Int: value}, true
	case *ast.BoolLit:
		return semantic.ConstValue{Kind: semantic.ConstBool, Bool: n.Value}, true
	case *ast.StringLit:
		return semantic.ConstValue{Kind: semantic.ConstString, String: n.Value}, true
	case *ast.Ident:
		if lookup != nil {
			if value, ok := lookup(n.Name); ok {
				return value, true
			}
		}
		return semantic.ConstValue{}, false
	case *ast.ParenExpr:
		return evalConstExprWithLookup(n.Inner, lookup)
	case *ast.MoveExpr:
		return evalConstExprWithLookup(n.Operand, lookup)
	case *ast.UnaryExpr:
		operand, ok := evalConstExprWithLookup(n.Operand, lookup)
		if !ok {
			return semantic.ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			if operand.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: !operand.Bool}, true
		case lexer.TOKEN_MINUS:
			if operand.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: -operand.Int}, true
		case lexer.TOKEN_TILDE:
			if operand.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: ^operand.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := evalConstExprWithLookup(n.Left, lookup)
		if !ok {
			return semantic.ConstValue{}, false
		}
		right, ok := evalConstExprWithLookup(n.Right, lookup)
		if !ok {
			return semantic.ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_AND:
			if left.Kind != semantic.ConstBool || right.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Bool && right.Bool}, true
		case lexer.TOKEN_OR:
			if left.Kind != semantic.ConstBool || right.Kind != semantic.ConstBool {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Bool || right.Bool}, true
		case lexer.TOKEN_EQEQ:
			return evalConstEquality(left, right, true)
		case lexer.TOKEN_BANGEQ:
			return evalConstEquality(left, right, false)
		case lexer.TOKEN_PLUS:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int + right.Int}, true
		case lexer.TOKEN_MINUS:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int - right.Int}, true
		case lexer.TOKEN_STAR:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int * right.Int}, true
		case lexer.TOKEN_SLASH:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt || right.Int == 0 {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int / right.Int}, true
		case lexer.TOKEN_PERCENT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt || right.Int == 0 {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int % right.Int}, true
		case lexer.TOKEN_LT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int < right.Int}, true
		case lexer.TOKEN_GT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int > right.Int}, true
		case lexer.TOKEN_LTEQ:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int <= right.Int}, true
		case lexer.TOKEN_GTEQ:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstBool, Bool: left.Int >= right.Int}, true
		case lexer.TOKEN_LSHIFT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int << right.Int}, true
		case lexer.TOKEN_RSHIFT:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int >> right.Int}, true
		case lexer.TOKEN_AMPERSAND:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int & right.Int}, true
		case lexer.TOKEN_PIPE:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int | right.Int}, true
		case lexer.TOKEN_CARET:
			if left.Kind != semantic.ConstInt || right.Kind != semantic.ConstInt {
				return semantic.ConstValue{}, false
			}
			return semantic.ConstValue{Kind: semantic.ConstInt, Int: left.Int ^ right.Int}, true
		default:
			return semantic.ConstValue{}, false
		}
	case *ast.TernaryExpr:
		condValue, ok := evalConstExprWithLookup(n.Cond, lookup)
		if !ok || condValue.Kind != semantic.ConstBool {
			return semantic.ConstValue{}, false
		}
		if condValue.Bool {
			return evalConstExprWithLookup(n.Value, lookup)
		}
		return evalConstExprWithLookup(n.Alt, lookup)
	default:
		return semantic.ConstValue{}, false
	}
}

func evalConstEquality(left, right semantic.ConstValue, equal bool) (semantic.ConstValue, bool) {
	matched := false
	switch {
	case left.Kind == semantic.ConstInt && right.Kind == semantic.ConstInt:
		matched = left.Int == right.Int
	case left.Kind == semantic.ConstBool && right.Kind == semantic.ConstBool:
		matched = left.Bool == right.Bool
	case left.Kind == semantic.ConstString && right.Kind == semantic.ConstString:
		matched = left.String == right.String
	default:
		return semantic.ConstValue{}, false
	}
	if !equal {
		matched = !matched
	}
	return semantic.ConstValue{Kind: semantic.ConstBool, Bool: matched}, true
}

func isZeroedExpr(expr ast.Expr) bool {
	_, ok := expr.(*ast.ZeroedLit)
	return ok
}

func isStaticallyZeroFillExpr(s *functionState, expr ast.Expr) bool {
	if isZeroedExpr(expr) {
		return true
	}
	if s == nil {
		return false
	}
	value, ok := s.evalConstExpr(expr)
	if !ok {
		return false
	}
	switch value.Kind {
	case semantic.ConstInt:
		return value.Int == 0
	case semantic.ConstBool:
		return !value.Bool
	default:
		return false
	}
}

func staticRepeatedByteFillValue(s *functionState, expr ast.Expr) (uint8, bool) {
	if isZeroedExpr(expr) {
		return 0, true
	}
	if s == nil {
		return 0, false
	}
	value, ok := s.evalConstExpr(expr)
	if !ok {
		return 0, false
	}
	exprType := s.exprType(expr)
	if exprType == nil {
		return 0, false
	}
	sizeBytes, err := s.sizeOfType(exprType)
	if err != nil || sizeBytes == 0 || sizeBytes > 8 {
		return 0, false
	}
	switch value.Kind {
	case semantic.ConstBool:
		if sizeBytes != 1 {
			return 0, false
		}
		if value.Bool {
			return 1, true
		}
		return 0, true
	case semantic.ConstInt:
		if !semantic.IsNumericType(exprType) {
			return 0, false
		}
		raw := uint64(value.Int)
		bitWidth := sizeBytes * 8
		if bitWidth < 64 {
			raw &= (uint64(1) << bitWidth) - 1
		}
		fillByte := uint8(raw & 0xff)
		for i := uint64(1); i < sizeBytes; i++ {
			if uint8((raw>>(8*i))&0xff) != fillByte {
				return 0, false
			}
		}
		return fillByte, true
	default:
		return 0, false
	}
}

func isSingleByteScalarFillType(s *functionState, t semantic.Type) bool {
	if s == nil || t == nil {
		return false
	}
	if !semantic.IsNumericType(t) && !semantic.IsBoolType(t) {
		return false
	}
	sizeBytes, err := s.sizeOfType(t)
	if err != nil {
		return false
	}
	return sizeBytes == 1
}

func isVoidType(t semantic.Type) bool {
	b, ok := t.(*semantic.BuiltinType)
	return ok && b.Name == "void"
}

func isNumericType(t semantic.Type) bool {
	return semantic.IsNumericType(t)
}

func isPointerLikeType(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.RefType, *semantic.NullType, *semantic.DStrType, *semantic.FuncType:
		return true
	default:
		return false
	}
}

func isSignedIntegerType(t semantic.Type) bool {
	b, ok := t.(*semantic.BuiltinType)
	if !ok {
		if _, ok := t.(*semantic.NullType); ok {
			return false
		}
		return false
	}
	switch b.Name {
	case "char", "int", "isize", "i8", "i16", "i32", "i64":
		return true
	default:
		return false
	}
}

func integerBitWidth(t semantic.Type, wordBits int) int {
	b, ok := t.(*semantic.BuiltinType)
	if !ok {
		if isPointerLikeType(t) {
			return wordBits
		}
		return wordBits
	}
	switch b.Name {
	case "bool":
		return 1
	case "i8", "u8":
		return 8
	case "i16", "u16":
		return 16
	case "i32", "u32":
		return 32
	case "char", "i64", "u64":
		return 64
	case "int", "isize", "usize", "uintptr":
		return wordBits
	default:
		return wordBits
	}
}

func llvmIntPredicate(op lexer.TokenKind, operandType semantic.Type) (C.LLVMIntPredicate, error) {
	signed := isSignedIntegerType(operandType)
	switch op {
	case lexer.TOKEN_EQEQ:
		return C.LLVMIntEQ, nil
	case lexer.TOKEN_BANGEQ:
		return C.LLVMIntNE, nil
	case lexer.TOKEN_LT:
		if signed {
			return C.LLVMIntSLT, nil
		}
		return C.LLVMIntULT, nil
	case lexer.TOKEN_GT:
		if signed {
			return C.LLVMIntSGT, nil
		}
		return C.LLVMIntUGT, nil
	case lexer.TOKEN_LTEQ:
		if signed {
			return C.LLVMIntSLE, nil
		}
		return C.LLVMIntULE, nil
	case lexer.TOKEN_GTEQ:
		if signed {
			return C.LLVMIntSGE, nil
		}
		return C.LLVMIntUGE, nil
	default:
		return 0, fmt.Errorf("unsupported comparison operator %s", lexer.TokenName(op))
	}
}

func cStringFree(s string) *C.char {
	if s == "" {
		return nil
	}
	ptr := C.CString(s)
	return ptr
}
