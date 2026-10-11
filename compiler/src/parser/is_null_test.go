package parser

import (
	"elisacore/src/ast"
	"testing"
)

func TestIsNullUsesComparableLiteralTarget(t *testing.T) {
	for _, condition := range []string{"value is null", "value is not null", "value is [null]"} {
		t.Run(condition, func(t *testing.T) {
			file, errs := parseSourceFile(t, "def absent(value: i64&?) -> bool:\n    return "+condition+"\n")
			if len(errs) != 0 {
				t.Fatalf("null test rejected: %v", errs)
			}
			value := file.Decls[0].(*ast.FuncDecl).Body[0].(*ast.ReturnStmt).Value
			if negation, ok := value.(*ast.UnaryExpr); ok {
				value = negation.Operand
			}
			target := value.(*ast.BinaryExpr).Right
			if pattern, ok := target.(*ast.IsPatternExpr); ok {
				if len(pattern.Targets) != 1 {
					t.Fatalf("null alternative lost: %#v", pattern)
				}
				target = pattern.Targets[0]
			}
			if _, ok := target.(*ast.NullLit); !ok {
				t.Fatalf("null parsed as a type or binder: %T", target)
			}
		})
	}
}
