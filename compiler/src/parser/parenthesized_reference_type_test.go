package parser

import (
	"testing"

	"elisacore/src/ast"
)

func TestParseParenthesizedReferenceCapabilities(t *testing.T) {
	file, errs := parseSourceFile(t, `def inner_mutable(pointer: (mutable i32&) &) -> void:
	pass

def outer_mutable(pointer: mutable (i32&) &) -> void:
	pass

def both_layers_mutable(pointer: mutable (mutable i32&?) &?) -> void:
	pass
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 3 {
		t.Fatalf("expected three function declarations, got %d", len(file.Decls))
	}

	innerFn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(innerFn.Params) != 1 {
		t.Fatalf("expected inner_mutable function with one parameter, got %#v", file.Decls[0])
	}
	outerRef, ok := innerFn.Params[0].Type.(*ast.RefType)
	if !ok {
		t.Fatalf("expected outer readonly RefType, got %T", innerFn.Params[0].Type)
	}
	innerMutable, ok := outerRef.Elem.(*ast.MutableType)
	if !ok {
		t.Fatalf("expected mutability to remain inside outer reference, got %T", outerRef.Elem)
	}
	if _, ok := innerMutable.Elem.(*ast.RefType); !ok {
		t.Fatalf("expected nested mutable reference, got %T", innerMutable.Elem)
	}

	outerFn, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok || len(outerFn.Params) != 1 {
		t.Fatalf("expected outer_mutable function with one parameter, got %#v", file.Decls[1])
	}
	outerMutable, ok := outerFn.Params[0].Type.(*ast.MutableType)
	if !ok {
		t.Fatalf("expected outer mutability wrapper, got %T", outerFn.Params[0].Type)
	}
	outerRef, ok = outerMutable.Elem.(*ast.RefType)
	if !ok {
		t.Fatalf("expected mutable outer RefType, got %T", outerMutable.Elem)
	}
	if _, ok := outerRef.Elem.(*ast.RefType); !ok {
		t.Fatalf("expected inner readonly reference, got %T", outerRef.Elem)
	}

	bothFn, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok || len(bothFn.Params) != 1 {
		t.Fatalf("expected both_layers_mutable function with one parameter, got %#v", file.Decls[2])
	}
	outerMutable, ok = bothFn.Params[0].Type.(*ast.MutableType)
	if !ok {
		t.Fatalf("expected writable outer reference, got %T", bothFn.Params[0].Type)
	}
	outerRef, ok = outerMutable.Elem.(*ast.RefType)
	if !ok {
		t.Fatalf("expected outer RefType, got %T", outerMutable.Elem)
	}
	innerMutable, ok = outerRef.Elem.(*ast.MutableType)
	if !ok {
		t.Fatalf("expected writable inner reference, got %T", outerRef.Elem)
	}
	if _, ok := innerMutable.Elem.(*ast.RefType); !ok {
		t.Fatalf("expected inner RefType, got %T", innerMutable.Elem)
	}
	innerRef, ok := innerMutable.Elem.(*ast.RefType)
	if !ok || innerRef.State != ast.RefStateNullable {
		t.Fatalf("expected inner nullable pointer layer, got %#v", innerMutable.Elem)
	}
	if outerRef.State != ast.RefStateNullable {
		t.Fatalf("expected outer nullable pointer layer, got %#v", outerRef)
	}
}
