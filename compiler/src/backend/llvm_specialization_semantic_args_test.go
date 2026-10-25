//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestOrderedSemanticTypeArgsIgnoreEnclosingImplBindings(t *testing.T) {
	concrete := &semantic.BuiltinType{Name: "i64"}
	bindings := map[string]semantic.Type{"T": concrete}
	// ensureSpecializedImplMethod specializes the signature from enclosing
	// impl parameters, while the method declares no generic params of its own.
	method := &ast.FuncDecl{Name: "method"}
	base := &semantic.FuncType{Name: "method", Params: []semantic.Type{&semantic.TypeParamType{Name: "T"}}, Return: &semantic.TypeParamType{Name: "T"}}
	specialized := specializeFuncType(base, bindings, nil)
	if len(bindings) == 0 {
		t.Fatal("fixture must have enclosing bindings")
	}
	if args := orderedGenericTypeArgs(specialized, method, bindings); len(args) != 0 {
		t.Fatal("enclosing impl bindings became declaration semantic arguments")
	}
	if specialized.Params[0] != concrete || specialized.Return != concrete {
		t.Fatal("backend signature substitution was lost")
	}
	// An ordinary generic declaration still receives its concrete arguments
	// even though specializeFuncType has erased the signature's generic list.
	generic := &ast.FuncDecl{Name: "generic", GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}}}
	args := orderedGenericTypeArgs(specialized, generic, bindings)
	if len(args) != 1 || args[0] != concrete {
		t.Fatal("actual declaration semantic specialization was skipped")
	}
}
