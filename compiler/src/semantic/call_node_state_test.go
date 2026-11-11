package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

func TestCallNodeStatePreservesFlagsAndSharedChildren(t *testing.T) {
	ident := &ast.Ident{Name: "callee"}
	call := &ast.CallExpr{
		Position: lexer.Pos{File: "call.elisa", Line: 7, Col: 3},
		Func:     ident, SafeReceiver: ident,
		ArgForwardPos: lexer.Pos{File: "call.elisa", Line: 7, Col: 11},
		Args:          []ast.Expr{ident}, ArgNames: []string{"value"}, ArgShorthand: []bool{true},
		ResolvedArgs:         []ast.Expr{ident},
		ResolvedCommonArgs:   map[string]ast.Expr{"value": ident},
		ResolvedImplicitArgs: []ast.Expr{ident},
	}
	value := reflect.ValueOf(call).Elem()
	var flags []int
	for i := 0; i < value.NumField(); i++ {
		if value.Field(i).Kind() == reflect.Bool {
			flags = append(flags, i)
		}
	}
	for active := -1; active <= len(flags); active++ {
		for i, field := range flags {
			value.Field(field).SetBool(i == active || active == len(flags))
		}
		data, err := semanticStateRegistry().Encode(call)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := semanticStateRegistry().Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		got := restored.(*ast.CallExpr)
		if !reflect.DeepEqual(call, got) {
			t.Fatalf("flags case %d changed call state", active)
		}
		if got.Func != got.SafeReceiver || got.Func != got.Args[0] || got.Func != got.ResolvedArgs[0] || got.Func != got.ResolvedCommonArgs["value"] || got.Func != got.ResolvedImplicitArgs[0] {
			t.Fatal("call state lost shared child identity")
		}
	}
}
