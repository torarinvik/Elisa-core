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

func TestParseExtensionImplAndValueMethodCall(t *testing.T) {
	file, errs := parseSourceFile(t, `
const enum Tok of i8:
	PLUS = 0

impl Tok:
	def score(self: Tok) -> i64:
		return 7

def read(tok: Tok) -> i64:
	return tok.score()
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 3 {
		t.Fatalf("expected 3 top-level decls, got %d", len(file.Decls))
	}
	impl, ok := file.Decls[1].(*ast.ImplDecl)
	if !ok {
		t.Fatalf("expected impl decl, got %T", file.Decls[1])
	}
	if !impl.IsExtension() {
		t.Fatalf("expected receiver-scoped extension impl, got interface impl %#v", impl)
	}
	receiver, ok := impl.ForType.(*ast.NamedType)
	if !ok || receiver.Name != "Tok" {
		t.Fatalf("expected extension receiver Tok, got %T %#v", impl.ForType, impl.ForType)
	}
	if len(impl.Members) != 1 {
		t.Fatalf("expected 1 extension method, got %d", len(impl.Members))
	}
	method, ok := impl.Members[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function member, got %T", impl.Members[0])
	}
	if method.Name != "score" || len(method.Params) != 1 || method.Params[0].Name != "self" {
		t.Fatalf("expected extension method score(self: Tok), got %#v", method)
	}
	funcDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[2])
	}
	ret, ok := funcDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", funcDecl.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected method call, got %T", ret.Value)
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field callee, got %T", call.Func)
	}
	owner, ok := callee.Object.(*ast.Ident)
	if !ok || owner.Name != "tok" || callee.Field != "score" {
		t.Fatalf("expected callee tok.score, got %T %#v", callee.Object, callee)
	}
	if len(call.Args) != 0 {
		t.Fatalf("expected zero explicit call args, got %d", len(call.Args))
	}
}

func TestParseDerivedImplAnnotationAndOverrideMethod(t *testing.T) {
	file, errs := parseSourceFile(t, `
struct BuilderTag:
    tag: int

interface Builder:
    type Node
    def make(value: int) -> Node

@derive(parse_builder tree Lua)
impl Builder for BuilderTag:
    type Node = int

    override def make(value: int) -> int:
        return value
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 3 {
		t.Fatalf("expected 3 top-level decls, got %d", len(file.Decls))
	}
	impl, ok := file.Decls[2].(*ast.ImplDecl)
	if !ok {
		t.Fatalf("expected impl decl, got %T", file.Decls[2])
	}
	if len(impl.Annotations) != 1 {
		t.Fatalf("expected 1 impl annotation, got %d", len(impl.Annotations))
	}
	annotation := impl.Annotations[0]
	if annotation.Name != "derive" {
		t.Fatalf("expected @derive annotation, got %q", annotation.Name)
	}
	if len(annotation.Args) != 3 || annotation.Args[0] != "parse_builder" || annotation.Args[1] != "tree" || annotation.Args[2] != "Lua" {
		t.Fatalf("expected spaced derive args [parse_builder tree Lua], got %#v", annotation.Args)
	}
	if len(impl.Members) != 2 {
		t.Fatalf("expected 2 impl members, got %d", len(impl.Members))
	}
	method, ok := impl.Members[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func impl member, got %T", impl.Members[1])
	}
	if !method.Override {
		t.Fatal("expected override def to set FuncDecl.Override")
	}
}

func TestParseStaticInterfaceTupleReturnAndDestructure(t *testing.T) {
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

def build_pair[B: Builder](value: int) -> (node: B.Node, checksum: int):
    return B.make(value), value

def use_pair[B: Builder](value: int) -> B.Node:
    built = build_pair.specialize[B]()(value)
    node, checksum = built
    _ = checksum
    return node
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 5 {
		t.Fatalf("expected 5 top-level decls, got %d", len(file.Decls))
	}
	buildDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected build_pair decl, got %T", file.Decls[3])
	}
	retType, ok := buildDecl.ReturnType.(*ast.TupleTypeExpr)
	if !ok {
		t.Fatalf("expected tuple return type, got %T", buildDecl.ReturnType)
	}
	if len(retType.Fields) != 2 || retType.Fields[0].Name != "node" || retType.Fields[1].Name != "checksum" {
		t.Fatalf("unexpected tuple return fields: %#v", retType.Fields)
	}
	retStmt, ok := buildDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", buildDecl.Body[0])
	}
	retTuple, ok := retStmt.Value.(*ast.TupleExpr)
	if !ok {
		t.Fatalf("expected tuple return expr, got %T", retStmt.Value)
	}
	if len(retTuple.Elems) != 2 {
		t.Fatalf("expected 2 tuple return elems, got %d", len(retTuple.Elems))
	}
	useDecl, ok := file.Decls[4].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected use_pair decl, got %T", file.Decls[4])
	}
	tupleBind, ok := useDecl.Body[1].(*ast.TupleBindStmt)
	if !ok {
		t.Fatalf("expected tuple destructuring stmt, got %T", useDecl.Body[1])
	}
	if !tupleBind.Declare {
		t.Fatal("expected tuple destructuring stmt to declare locals")
	}
	if len(tupleBind.Names) != 2 || tupleBind.Names[0].Name != "node" || tupleBind.Names[1].Name != "checksum" {
		t.Fatalf("unexpected tuple bind names: %#v", tupleBind.Names)
	}
	if ident, ok := tupleBind.Value.(*ast.Ident); !ok || ident.Name != "built" {
		t.Fatalf("expected tuple bind source to be built, got %T %#v", tupleBind.Value, tupleBind.Value)
	}
}
