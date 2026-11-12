package parsersyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestMachineLiteralKeys(t *testing.T) {
	cases := []struct {
		expr         ast.Expr
		key, display string
	}{
		{&ast.IntLit{Value: "42"}, "int:42", "42"},
		{&ast.BoolLit{Value: true}, "bool:true", "true"},
		{&ast.ShorthandMemberExpr{Parts: []string{"State", "Ready"}}, "tag:.State.Ready", ".State.Ready"},
	}
	for _, tc := range cases {
		key, ok := MachineLiteralKey(tc.expr)
		if !ok || key != tc.key || MachineInputLiteralDisplay(key) != tc.display {
			t.Errorf("got %q %v", key, ok)
		}
	}
}
