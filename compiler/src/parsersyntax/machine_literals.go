package parsersyntax

import (
	"elisacore/src/ast"
	"strings"
)

func MachineLiteralKey(e ast.Expr) (string, bool) {
	switch n := e.(type) {
	case *ast.CharLit:
		return "char:" + n.Value, true
	case *ast.IntLit:
		if n.IsHex {
			return "int:hex:" + n.Value, true
		}
		return "int:" + n.Value, true
	case *ast.FloatLit:
		return "float:" + n.Value, true
	case *ast.StringLit:
		return "str:" + n.Value, true
	case *ast.BoolLit:
		if n.Value {
			return "bool:true", true
		}
		return "bool:false", true
	case *ast.UnaryExpr:
		if key, ok := MachineLiteralKey(n.Operand); ok {
			// The machine-value grammar currently admits only unary minus here. Keep the
			// operator out of the key: the operand key is already distinct from every
			// direct literal key, and this avoids depending on TokenKind stringification.
			return "unary:" + key, true
		}
	case *ast.ParenExpr:
		return MachineLiteralKey(n.Inner)
	case *ast.FieldExpr:
		// A qualified enum member `Enum.Variant` (classified-dispatch tags).
		if obj, ok := n.Object.(*ast.Ident); ok {
			return "tag:" + obj.Name + "." + n.Field, true
		}
	case *ast.ShorthandMemberExpr:
		// A leading-dot enum member `.Variant` (classified-dispatch tag). Keep the
		// complete shorthand path so repeated spellings are rejected exactly without
		// conflating it with a qualified member from an unrelated enum.
		if len(n.Parts) > 0 {
			return "tag:." + strings.Join(n.Parts, "."), true
		}
	}
	return "", false
}

// machineInputLiteralDisplay renders a coverage key back to a readable form for diagnostics.
func MachineInputLiteralDisplay(key string) string {
	switch {
	case strings.HasPrefix(key, "char:"):
		return "'" + strings.TrimPrefix(key, "char:") + "'"
	case strings.HasPrefix(key, "int:hex:"):
		return strings.TrimPrefix(key, "int:hex:")
	case strings.HasPrefix(key, "int:"):
		return strings.TrimPrefix(key, "int:")
	case strings.HasPrefix(key, "float:"):
		return strings.TrimPrefix(key, "float:")
	case strings.HasPrefix(key, "unary:"):
		return "-" + MachineInputLiteralDisplay(strings.TrimPrefix(key, "unary:"))
	case strings.HasPrefix(key, "str:"):
		return "\"" + strings.TrimPrefix(key, "str:") + "\""
	case strings.HasPrefix(key, "bool:"):
		return strings.TrimPrefix(key, "bool:")
	case strings.HasPrefix(key, "tag:"):
		return strings.TrimPrefix(key, "tag:")
	}
	return key
}
