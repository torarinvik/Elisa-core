//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"strconv"
	"strings"
)

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
func staticRepeatedByteFillValueForType(s *functionState, expr ast.Expr, fillType semantic.Type) (uint8, bool) {
	if isZeroedExpr(expr) {
		return 0, true
	}
	if s == nil || fillType == nil {
		return 0, false
	}
	value, ok := s.evalConstExpr(expr)
	if !ok {
		return 0, false
	}
	sizeBytes, err := s.sizeOfType(fillType)
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
		if !semantic.IsNumericType(fillType) {
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
func constOptimizationExtentSize(extent *semantic.OptimizationExtent) (uint64, bool) {
	if extent == nil {
		return 0, false
	}
	switch extent.Kind {
	case semantic.OptimizationExtentArraySize:
		if extent.HasConstSize && extent.ConstSize >= 0 {
			return uint64(extent.ConstSize), true
		}
		return 0, false
	case semantic.OptimizationExtentViewBounds:
		begin, ok := parseOptimizationExtentConstInt(extent.Begin)
		if !ok {
			return 0, false
		}
		end, ok := parseOptimizationExtentConstInt(extent.End)
		if !ok || end < begin {
			return 0, false
		}
		return uint64(end - begin), true
	default:
		return 0, false
	}
}
func parseOptimizationExtentConstInt(value string) (int64, bool) {
	trimmed := strings.TrimSpace(value)
	for len(trimmed) >= 2 && trimmed[0] == '(' && trimmed[len(trimmed)-1] == ')' {
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	if trimmed == "" {
		return 0, false
	}
	if last := trimmed[len(trimmed)-1]; last == 'u' || last == 'i' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}
func isVoidType(t semantic.Type) bool {
	b, ok := t.(*semantic.BuiltinType)
	return ok && b.Name == "void"
}
func isNumericType(t semantic.Type) bool {
	return semantic.IsNumericType(t)
}
func numericCastType(t semantic.Type) semantic.Type {
	if storage, ok := semantic.ConstEnumStorageType(t); ok {
		return storage
	}
	if idType, ok := t.(*semantic.IDType); ok && idType != nil && idType.Storage != nil {
		return idType.Storage
	}
	return t
}
func isNumericCastType(t semantic.Type) bool {
	return semantic.IsNumericType(numericCastType(t))
}
func isUintptrType(t semantic.Type) bool {
	b, ok := t.(*semantic.BuiltinType)
	return ok && b != nil && b.Name == "uintptr"
}
func isPointerLikeType(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.RefType, *semantic.NullType, *semantic.CStrType, *semantic.FuncType:
		return true
	default:
		return false
	}
}
func isSignedIntegerType(t semantic.Type) bool {
	t = numericCastType(t)
	if signed, _, ok := semantic.BitIntInfo(t); ok {
		return signed
	}
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
func isFloatType(t semantic.Type) bool {
	t = numericCastType(t)
	return semantic.IsFloatType(t)
}
func integerBitWidth(t semantic.Type, wordBits int) int {
	t = numericCastType(t)
	if _, width, ok := semantic.BitIntInfo(t); ok {
		return width
	}
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
	case "f32":
		return 32
	case "char", "i64", "u64":
		return 64
	case "f64":
		return 64
	case "int", "isize", "usize", "uintptr":
		return wordBits
	default:
		return wordBits
	}
}
func llvmIntPredicate(op lexer.TokenKind, operandType semantic.Type) (llvmIRIntPredicate, error) {
	signed := isSignedIntegerType(operandType)
	switch op {
	case lexer.TOKEN_EQEQ:
		return llvmIntEQ, nil
	case lexer.TOKEN_BANGEQ:
		return llvmIntNE, nil
	case lexer.TOKEN_LT:
		if signed {
			return llvmIntSLT, nil
		}
		return llvmIntULT, nil
	case lexer.TOKEN_GT:
		if signed {
			return llvmIntSGT, nil
		}
		return llvmIntUGT, nil
	case lexer.TOKEN_LTEQ:
		if signed {
			return llvmIntSLE, nil
		}
		return llvmIntULE, nil
	case lexer.TOKEN_GTEQ:
		if signed {
			return llvmIntSGE, nil
		}
		return llvmIntUGE, nil
	default:
		return 0, fmt.Errorf("unsupported comparison operator %s", lexer.TokenName(op))
	}
}
func llvmFloatPredicate(op lexer.TokenKind) (llvmIRRealPredicate, error) {
	switch op {
	case lexer.TOKEN_EQEQ:
		return llvmRealOEQ, nil
	case lexer.TOKEN_BANGEQ:
		// C/IEEE `!=` is unordered-not-equal: NaN != every value, including
		// another NaN. LLVMRealONE is ordered-not-equal and incorrectly returns
		// false for NaN.
		return llvmRealUNE, nil
	case lexer.TOKEN_LT:
		return llvmRealOLT, nil
	case lexer.TOKEN_GT:
		return llvmRealOGT, nil
	case lexer.TOKEN_LTEQ:
		return llvmRealOLE, nil
	case lexer.TOKEN_GTEQ:
		return llvmRealOGE, nil
	default:
		return 0, fmt.Errorf("unsupported comparison operator %s", lexer.TokenName(op))
	}
}
func cStringFree(s string) *llvmIRChar {
	if s == "" {
		return nil
	}
	moduleCStringState.Lock()
	defer moduleCStringState.Unlock()
	if ptr := moduleCStringState.byName[s]; ptr != nil {
		return ptr
	}
	ptr := llvmCString(s)
	if moduleCStringState.byName == nil {
		// Production callers are enclosed by begin/endModuleCStringLifetime.
		// Keep the helper total for isolated backend tests too; those tests must
		// use the same lifetime pair when they exercise LLVM builders.
		moduleCStringState.byName = make(map[string]*llvmIRChar)
	}
	moduleCStringState.byName[s] = ptr
	moduleCStringState.owned = append(moduleCStringState.owned, ptr)
	return ptr
}
