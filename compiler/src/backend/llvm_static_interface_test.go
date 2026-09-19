//go:build cgo

package backend

import (
	"strings"
	"testing"

	"elisacore/src/semantic"
)

func TestGenerateLLVMIRPreservesImplLocalSelf(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "impl_local_self.elisa", `
protocol Identity:
    def identity(self: Self) -> Self
struct Small:
    value: i64
struct Large:
    first: i64
    second: i64
impl Identity for Small:
    def identity(self: Self) -> Self:
        copy: Self = self
        return copy
impl Identity for Large:
    def identity(self: Self) -> Self:
        copy: Self = self
        return copy
impl Small:
    def copied(self: Self) -> Self:
        copy: Self = self
        return copy
def entry() -> i64:
    return 0
`)
	if _, err := generateLLVMIRWithDefaultPackedLoweringForTest(result); err != nil {
		t.Fatalf("local Self must retain its own implementation's receiver: %v", err)
	}
}

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
        return AstNode{value: value}

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
