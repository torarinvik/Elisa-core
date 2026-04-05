package parser

import (
	"testing"

	"llcontext/src/ast"
)

func TestParseStaticInterfaceImplAndBoundedGeneric(t *testing.T) {
	file, errs := parseSourceFile(t, `
struct BuilderTag:
    tag: int

interface Builder:
    type Node
    def make(value: int) -> Node

impl Builder for BuilderTag:
    type Node = int
    def make(value: int) -> int:
        return value

def build[B: Builder](value: int) -> B.Node:
    return B.make(value)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 4 {
		t.Fatalf("expected 4 top-level decls, got %d", len(file.Decls))
	}
	iface, ok := file.Decls[1].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("expected interface decl, got %T", file.Decls[1])
	}
	if iface.Name != "Builder" {
		t.Fatalf("expected interface Builder, got %q", iface.Name)
	}
	if len(iface.Members) != 2 {
		t.Fatalf("expected 2 interface members, got %d", len(iface.Members))
	}
	assoc, ok := iface.Members[0].(*ast.AssociatedTypeDecl)
	if !ok || assoc.Name != "Node" {
		t.Fatalf("expected associated type Node, got %T %#v", iface.Members[0], iface.Members[0])
	}
	method, ok := iface.Members[1].(*ast.ExternFuncDecl)
	if !ok || method.Name != "make" {
		t.Fatalf("expected interface method make, got %T %#v", iface.Members[1], iface.Members[1])
	}
	impl, ok := file.Decls[2].(*ast.ImplDecl)
	if !ok {
		t.Fatalf("expected impl decl, got %T", file.Decls[2])
	}
	if impl.InterfaceName != "Builder" {
		t.Fatalf("expected impl for Builder, got %q", impl.InterfaceName)
	}
	forType, ok := impl.ForType.(*ast.NamedType)
	if !ok || forType.Name != "BuilderTag" {
		t.Fatalf("expected impl target BuilderTag, got %T %#v", impl.ForType, impl.ForType)
	}
	if len(impl.Members) != 2 {
		t.Fatalf("expected 2 impl members, got %d", len(impl.Members))
	}
	funcDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected generic function decl, got %T", file.Decls[3])
	}
	if len(funcDecl.GenericParams) != 1 {
		t.Fatalf("expected one generic param, got %d", len(funcDecl.GenericParams))
	}
	if param := funcDecl.GenericParams[0]; param.Name != "B" || param.InterfaceBound != "Builder" {
		t.Fatalf("expected generic bound B: Builder, got %#v", param)
	}
	retType, ok := funcDecl.ReturnType.(*ast.NamedType)
	if !ok || retType.Name != "B.Node" {
		t.Fatalf("expected return type B.Node, got %T %#v", funcDecl.ReturnType, funcDecl.ReturnType)
	}
}

func TestParseStaticInterfaceZeroArgMethodCall(t *testing.T) {
	file, errs := parseSourceFile(t, `
struct BuilderTag:
    tag: int

interface Builder:
    type State
    def state() -> State

impl Builder for BuilderTag:
    type State = int

    def state() -> int:
        return 1

def build[B: Builder]() -> B.State:
    return B.state()
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	funcDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[3])
	}
	ret, ok := funcDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", funcDecl.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected zero-arg method call, got %T", ret.Value)
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field callee, got %T", call.Func)
	}
	owner, ok := callee.Object.(*ast.Ident)
	if !ok || owner.Name != "B" || callee.Field != "state" {
		t.Fatalf("expected callee B.state, got %T %#v", callee.Object, callee)
	}
	if len(call.Args) != 0 {
		t.Fatalf("expected zero call args, got %d", len(call.Args))
	}
}
