package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeStaticInterfaceZeroArgMethodCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_zero_arg.elisa", `
struct BuilderTag:
    tag: int

protocol Builder:
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
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_zero_arg_local.elisa", `
struct BuilderTag:
    tag: int

protocol Builder:
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

func TestAnalyzeSpanAlgebraUsesSpanLikeProtocol(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "spanlike_protocol.elisa", `
struct Span:
    start: i64
    end: i64

protocol SpanLike:
    type Range
    def combine(left: Range, right: Range) -> Range

impl SpanLike for Span:
    type Range = Span

    def combine(left: Span, right: Span) -> Span:
        return Span{start: left.start, end: right.end}

def join(left: Span, right: Span) -> Span:
    return left + right
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeStaticInterfaceExplicitSpecializationWithBoundTypeParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_bound_forward.elisa", `
struct BuilderTag:
    tag: int

protocol Builder:
    type State
    def state() -> State

impl Builder for BuilderTag:
    type State = int

    def state() -> int:
        return 1

def inner[B: Builder]() -> B.State:
    return B.state()

def outer[B: Builder]() -> B.State:
    return inner[B]()
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeDerivedNullBuilderSynthesizesMissingMethods(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "derived_null_builder.elisa", `
struct SinkBuilder:
    tag: int

protocol Sink:
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
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_tuple_return.elisa", `
struct BuilderTag:
    tag: int

protocol Builder:
    type Node
    def make(value: int) -> Node

impl Builder for BuilderTag:
    type Node = int

    def make(value: int) -> int:
        return value

def build_pair[B: Builder](value: int) -> (node: B.Node, checksum: int):
    return B.make(value), value

def use_pair[B: Builder](value: int) -> B.Node:
    built = build_pair[B](value)
    node, checksum = built
    _ = checksum
    return node
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeStaticInterfaceRejectsUnknownInterface(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_interface_unknown.elisa", `
struct BuilderTag:
    tag: int

impl MissingBuilder for BuilderTag:
    def state() -> int:
        return 1
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, UnknownInterfaceMessage("MissingBuilder")) {
		t.Fatalf("expected unknown interface diagnostic, got:\n%s", joined)
	}
}

func TestAnalyzeProtocolCanRequireCastMethod(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "protocol_cast_requirement.elisa", `
protocol Str:
    def __cast__(self: Self) -> cstr can[Memory.Allocate, Console.Format, Abort.Panic]

struct Label:
    text: cstr

impl Str for Label:
    def __cast__(self: Label) -> cstr can[Memory.Allocate, Console.Format, Abort.Panic]:
        return self.text

def read[T: Str](value: T) -> cstr can[Memory.Allocate, Console.Format, Abort.Panic]:
    return T.__cast__(value) can Memory.Allocate, Console.Format, Abort.Panic
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}
