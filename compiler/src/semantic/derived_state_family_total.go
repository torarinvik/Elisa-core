package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A deliberately small, checked partition certificate. No assumed coverage,
// floating-point ordered complements (NaN), calls, or arithmetic rewrites.
func derivedStateFamilyTotal(base *StructType) bool {
	if base == nil || len(base.NamedStateCases) != len(base.DerivedStates) {
		return false
	}
	if len(base.DerivedStates) == 1 {
		lit, ok := stripOptimizationParens(base.DerivedStates[0].Condition).(*ast.BoolLit)
		return ok && lit.Value
	}
	if len(base.DerivedStates) != 2 {
		return false
	}
	left, lok := stripOptimizationParens(base.DerivedStates[0].Condition).(*ast.BinaryExpr)
	right, rok := stripOptimizationParens(base.DerivedStates[1].Condition).(*ast.BinaryExpr)
	if !lok || !rok || left.LoweredCall != nil || right.LoweredCall != nil ||
		!derivedPartitionIntegerOperand(base, left.Left) || !derivedPartitionIntegerOperand(base, left.Right) ||
		!derivedPartitionIntegerOperand(base, right.Left) || !derivedPartitionIntegerOperand(base, right.Right) ||
		!sameContractClause(left.Left, right.Left) || !sameContractClause(left.Right, right.Right) {
		return false
	}
	complement := map[lexer.TokenKind]lexer.TokenKind{
		lexer.TOKEN_GT: lexer.TOKEN_LTEQ, lexer.TOKEN_LTEQ: lexer.TOKEN_GT,
		lexer.TOKEN_LT: lexer.TOKEN_GTEQ, lexer.TOKEN_GTEQ: lexer.TOKEN_LT,
		lexer.TOKEN_EQEQ: lexer.TOKEN_BANGEQ, lexer.TOKEN_BANGEQ: lexer.TOKEN_EQEQ,
	}
	op, ok := complement[left.Op]
	if !ok || right.Op != op {
		return false
	}
	return derivedPartitionIntegerOperand(base, left.Left) && derivedPartitionIntegerOperand(base, left.Right)
}

func derivedPartitionIntegerOperand(base *StructType, expr ast.Expr) bool {
	expr = stripOptimizationParens(expr)
	if _, ok := expr.(*ast.IntLit); ok {
		return true
	}
	field, ok := expr.(*ast.FieldExpr)
	if !ok {
		return false
	}
	self, ok := field.Object.(*ast.Ident)
	if !ok || self.Name != "self" {
		return false
	}
	f, ok := base.Fields[field.Field]
	if !ok {
		return false
	}
	builtin, ok := f.Type.(*BuiltinType)
	if !ok {
		return false
	}
	switch builtin.Name {
	case "i8", "i16", "i32", "i64", "int", "u8", "u16", "u32", "u64", "usize", "uintptr":
		return true
	}
	return false
}
