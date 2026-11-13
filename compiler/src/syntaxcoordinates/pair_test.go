package syntaxcoordinates

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestPairPlansCoordinatesAndSkipsChangedBody(t *testing.T) {
	old := &ast.FuncDecl{Name: "f", Position: lexer.Pos{Line: 1}, Body: []ast.Stmt{&ast.ReturnStmt{Position: lexer.Pos{Line: 2}, Value: &ast.IntLit{Value: "1"}}}}
	next := &ast.FuncDecl{Name: "f", Position: lexer.Pos{Line: 5}, Body: []ast.Stmt{&ast.ReturnStmt{Position: lexer.Pos{Line: 6}, Value: &ast.IntLit{Value: "2"}}}}
	type update struct {
		field reflect.Value
		pos   lexer.Pos
	}
	var writes []update
	add := func(field reflect.Value, pos lexer.Pos) { writes = append(writes, update{field, pos}) }
	if err := Pair(old, next, false, add); err == nil {
		t.Fatal("changed body must be rejected")
	}
	writes = nil
	if err := Pair(old, next, true, add); err != nil {
		t.Fatal(err)
	}
	if old.Position.Line != 1 {
		t.Fatal("planning modified original")
	}
	for _, write := range writes {
		write.field.Set(reflect.ValueOf(write.pos))
	}
	if old.Position.Line != 5 || old.Body[0].(*ast.ReturnStmt).Position.Line != 2 {
		t.Fatal("incorrect coordinate update or changed body modified")
	}
}

func TestPairUpdatesUnchangedBodyCoordinates(t *testing.T) {
	old := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Position: lexer.Pos{Line: 2}, Value: &ast.IntLit{Position: lexer.Pos{Line: 2}, Value: "1"}}}}
	next := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Position: lexer.Pos{Line: 8}, Value: &ast.IntLit{Position: lexer.Pos{Line: 8}, Value: "1"}}}}
	var fields []reflect.Value
	var positions []lexer.Pos
	if err := Pair(old, next, false, func(field reflect.Value, pos lexer.Pos) {
		fields = append(fields, field)
		positions = append(positions, pos)
	}); err != nil {
		t.Fatal(err)
	}
	for i, field := range fields {
		field.Set(reflect.ValueOf(positions[i]))
	}
	ret := old.Body[0].(*ast.ReturnStmt)
	if ret.Position.Line != 8 || ret.Value.(*ast.IntLit).Position.Line != 8 {
		t.Fatal("body coordinate changes were not applied")
	}
}
