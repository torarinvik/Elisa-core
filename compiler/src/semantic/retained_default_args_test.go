package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestRetainedMentionsAnyIncludesParameterDefaults(t *testing.T) {
	fn := &ast.FuncDecl{
		Name: "caller",
		Params: []ast.ParamDecl{{
			Name: "value",
			DefaultValue: &ast.CallExpr{
				Func: &ast.Ident{Name: "removed_helper"},
			},
		}},
	}
	if !retainedMentionsAny(fn, map[string]bool{"removed_helper": true}) {
		t.Fatal("function default reference was omitted from retained invalidation")
	}
}
