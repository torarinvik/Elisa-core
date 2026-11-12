package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

func ParseStackBlockIntLit(text string) (int64, bool) {
	v, err := strconv.ParseInt(strings.ReplaceAll(text, "_", ""), 0, 64)
	return v, err == nil
}

// stackBlockBoundEvaluable accepts the bound shapes codegen evaluates without trapping:
// integer literals, parameters, `+`/`*`, parentheses, and integer conversions of those.
func StackBlockBoundEvaluable(e ast.Expr, paramNames map[string]bool) bool {
	switch b := e.(type) {
	case *ast.IntLit:
		return true
	case *ast.Ident:
		return paramNames[b.Name]
	case *ast.ParenExpr:
		return StackBlockBoundEvaluable(b.Inner, paramNames)
	case *ast.BinaryExpr:
		if b.Op != lexer.TOKEN_STAR && b.Op != lexer.TOKEN_PLUS {
			return false
		}
		return StackBlockBoundEvaluable(b.Left, paramNames) && StackBlockBoundEvaluable(b.Right, paramNames)
	case *ast.CastExpr:
		return StackBlockIntegerTypeName(b.Target) != "" && StackBlockBoundEvaluable(b.Operand, paramNames)
	case *ast.CallExpr:
		field, ok := b.Func.(*ast.FieldExpr)
		if !ok || len(b.Args) != 0 || !stackBlockIntegerName(field.Field) {
			return false
		}
		return StackBlockBoundEvaluable(field.Object, paramNames)
	}
	return false
}

func stackBlockIntegerName(name string) bool {
	switch name {
	case "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
		return true
	}
	return false
}

// StackBlockIntegerTypeName returns the builtin integer name a cast target spells, or "".
func StackBlockIntegerTypeName(t ast.TypeExpr) string {
	if nt, ok := t.(*ast.NamedType); ok && stackBlockIntegerName(nt.Name) {
		return nt.Name
	}
	return ""
}
