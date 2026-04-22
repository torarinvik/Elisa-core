package semantic

import "testing"

func TestAnalyzeStaticInterfaceZeroArgMethodCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_zero_arg.llcontext", `
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

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeStaticInterfaceZeroArgMethodCallWithAssociatedLocal(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_zero_arg_local.llcontext", `
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
    value: B.State = B.state()
    return value
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeStaticInterfaceExplicitSpecializationWithBoundTypeParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_bound_forward.llcontext", `
struct BuilderTag:
    tag: int

interface Builder:
    type State
    def state() -> State

impl Builder for BuilderTag:
    type State = int

    def state() -> int:
        return 1

def inner[B: Builder]() -> B.State:
    return B.state()

def outer[B: Builder]() -> B.State:
    return inner.specialize[B]()()
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeDerivedParseBuilderSynthesizesMissingMethods(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "derived_parse_builder.llcontext", `
tree Lua:
    common:
        span: i64
    @role(expr)
    node Expr:
        IntegerLit(value: i64)
        Binary(left: Expr, right: Expr)

struct LuaAstBuilder:
    tag: int

interface LuaBuilder:
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
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
	impl, ok := LookupStaticImpl(result.StaticImpls, "LuaBuilder", result.NamedTypes["LuaAstBuilder"])
	if !ok || impl == nil {
		t.Fatal("expected derived impl for LuaBuilder/LuaAstBuilder")
	}
	if impl.Methods["make_integer"] == nil {
		t.Fatal("expected derived impl method symbol for make_integer")
	}
	if impl.Methods["make_binary"] == nil {
		t.Fatal("expected derived impl method symbol for make_binary")
	}
}

func TestAnalyzeDerivedNullBuilderSynthesizesMissingMethods(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "derived_null_builder.llcontext", `
struct SinkBuilder:
    tag: int

interface Sink:
    type Node
    def make(value: int) -> Node
    def touch(node: Node)

@derive(null_builder)
impl Sink for SinkBuilder:
    type Node = int

def entry() -> int:
    value: int = SinkBuilder.make(7)
    SinkBuilder.touch(value)
    return value
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
	impl, ok := LookupStaticImpl(result.StaticImpls, "Sink", result.NamedTypes["SinkBuilder"])
	if !ok || impl == nil {
		t.Fatal("expected derived impl for Sink/SinkBuilder")
	}
	if impl.Methods["make"] == nil {
		t.Fatal("expected derived impl method symbol for make")
	}
	if impl.Methods["touch"] == nil {
		t.Fatal("expected derived impl method symbol for touch")
	}
}

func TestAnalyzeStaticInterfaceTupleReturnAndDestructure(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_tuple_return.llcontext", `
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

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}
