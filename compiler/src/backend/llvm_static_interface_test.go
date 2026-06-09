//go:build cgo

package backend

import (
	"strings"
	"testing"

	"elisacore/src/semantic"
)

func TestGenerateLLVMIRLowersStaticInterfaceMethodDispatch(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "static_interface_dispatch.elisa", `
struct AstNode:
    value: int

struct BuilderTag:
    tag: int

protocol Builder:
    type Node
    def make(value: int) -> Node
    def value_of(node: Node) -> int

impl Builder for BuilderTag:
    type Node = AstNode

    def make(value: int) -> AstNode:
        return AstNode(value)

    def value_of(node: AstNode) -> int:
        return node.value

def build_and_read[B: Builder](value: int) -> int:
    node: B.Node = B.make(value)
    return B.value_of(node)

def entry() -> int:
    return build_and_read[BuilderTag](41)
`)

	impl, ok := semantic.LookupStaticImpl(result.StaticImpls, "Builder", result.NamedTypes["BuilderTag"])
	if !ok || impl == nil {
		t.Fatal("expected BuilderTag impl for Builder")
	}
	makeSym := impl.Methods["make"]
	if makeSym == nil {
		t.Fatal("expected impl method symbol for make")
	}
	valueSym := impl.Methods["value_of"]
	if valueSym == nil {
		t.Fatal("expected impl method symbol for value_of")
	}

	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@entry") {
		t.Fatalf("expected generated IR to contain entry function, got:\n%s", output)
	}
	if !strings.Contains(output, "@"+makeSym.Name) {
		t.Fatalf("expected generated IR to contain impl make symbol %q, got:\n%s", makeSym.Name, output)
	}
	if !strings.Contains(output, "@"+valueSym.Name) {
		t.Fatalf("expected generated IR to contain impl value_of symbol %q, got:\n%s", valueSym.Name, output)
	}
}

func TestGenerateLLVMIRLowersDerivedParseBuilderImplMethods(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "static_interface_derived_parse_builder.elisa", `
@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		IntegerLit(value: i64)
		Binary(left: Expr, right: Expr)

struct LuaAstBuilder:
	tag: int

protocol LuaBuilder:
	type ExprNode
	def make_integer(alloc: mutable Arena&, span: i64, value: i64) -> ExprNode
	def make_binary(alloc: mutable Arena&, span: i64, left: ExprNode, right: ExprNode) -> ExprNode

@derive(parse_builder tree Lua)
impl LuaBuilder for LuaAstBuilder:
	type ExprNode = Lua.Expr

def build(owner: Arena) -> Lua.Expr:
	alloc: mutable Arena& = (&owner).cast[mutable Arena&]
	left: Lua.Expr = LuaAstBuilder.make_integer(alloc, 1, 2)
	right: Lua.Expr = LuaAstBuilder.make_integer(alloc, 1, 3)
	return LuaAstBuilder.make_binary(alloc, 1, left, right)

def entry(owner: Arena) -> i64:
	expr: Lua.Expr = build(owner)
	if expr is Lua.Expr.Binary:
		return expr.left.span + expr.right.span
	return -1
`)

	impl, ok := semantic.LookupStaticImpl(result.StaticImpls, "LuaBuilder", result.NamedTypes["LuaAstBuilder"])
	if !ok || impl == nil {
		t.Fatal("expected LuaAstBuilder impl for LuaBuilder")
	}
	makeIntegerSym := impl.Methods["make_integer"]
	if makeIntegerSym == nil {
		t.Fatal("expected impl method symbol for make_integer")
	}
	makeBinarySym := impl.Methods["make_binary"]
	if makeBinarySym == nil {
		t.Fatal("expected impl method symbol for make_binary")
	}

	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@entry") {
		t.Fatalf("expected generated IR to contain entry function, got:\n%s", output)
	}
	if !strings.Contains(output, "@"+makeIntegerSym.Name) {
		t.Fatalf("expected generated IR to contain derived impl make_integer symbol %q, got:\n%s", makeIntegerSym.Name, output)
	}
	if !strings.Contains(output, "@"+makeBinarySym.Name) {
		t.Fatalf("expected generated IR to contain derived impl make_binary symbol %q, got:\n%s", makeBinarySym.Name, output)
	}
}
