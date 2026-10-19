//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"strconv"
	"strings"
	"testing"
)

func TestGenericSymbolFramingAndBoundedNames(t *testing.T) {
	g := &llvmGenerator{}
	u8 := &semantic.BuiltinType{Name: "u8"}
	a, err := g.mangleGenericType("f__u8", []semantic.Type{u8})
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.mangleGenericType("f", []semantic.Type{u8, u8})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("base/arity boundary collided")
	}
	c, err := g.mangleGenericType(strings.Repeat("long", 2048), []semantic.Type{&semantic.RefType{Elem: u8}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c) > 128 {
		t.Fatal("emitted name is unbounded", len(c))
	}
	// Force the exact-key guard independently of the collision resistance of SHA.
	g.genericSymbolKeys[a] = "different structural authority"
	if _, err := g.mangleGenericType("f__u8", []semantic.Type{u8}); err == nil {
		t.Fatal("digest/full-key mismatch was accepted")
	}
	carrier, err := g.runtimeDynArrayName(&semantic.BuiltinType{Name: "uintptr"})
	if err != nil || carrier != "DynArray__uintptr" {
		t.Fatal(carrier, err)
	}
}

func TestGenericExternLegacyABICompatibility(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "generic_extern_identity.elisa", "def main() -> i64:\n    return 0\n")
	g, err := newLLVMGenerator(result)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	u8 := &semantic.BuiltinType{Name: "u8"}
	decl := &ast.ExternFuncDecl{Name: "probe", TypeParams: []string{"T"}}
	base := &semantic.FuncType{Name: "probe", TypeParams: []string{"T"}, Params: []semantic.Type{&semantic.TypeParamType{Name: "T"}}, Return: &semantic.BuiltinType{Name: "void"}, CallConv: "c", IsNativeExtern: true}
	refA := &semantic.RefType{Elem: u8}
	refB := &semantic.RefType{Elem: u8, State: semantic.RefStateNullable}
	if legacyGenericExternName("probe", []semantic.Type{refA}) != "probe__u8" || legacyGenericExternName("probe", []semantic.Type{refB}) != "probe__u8" {
		t.Fatal("ABI control does not share the exact legacy symbol")
	}
	keyA, err := semantic.TypeSymbolKey(refA)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := semantic.TypeSymbolKey(refB)
	if err != nil {
		t.Fatal(err)
	}
	if keyA == keyB {
		t.Fatal("nonnull and nullable structural identities collapsed")
	}
	first, _, err := g.ensureSpecializedExternFunction(decl, base, map[string]semantic.Type{"T": refA})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := g.ensureSpecializedExternFunction(decl, base, map[string]semantic.Type{"T": refB})
	if err != nil {
		t.Fatal("same ABI was rejected", err)
	}
	if first != second {
		t.Fatal("legacy symbol was duplicated")
	}
	if _, _, err := g.ensureSpecializedExternFunction(decl, base, map[string]semantic.Type{"T": u8}); err == nil {
		t.Fatal("incompatible pointer/scalar ABI was accepted")
	}
	incompatible := &semantic.FuncType{Params: []semantic.Type{refA}, Return: &semantic.BuiltinType{Name: "void"}, CallConv: "fast", IsNativeExtern: true}
	if err := g.checkGenericExternABI("probe__u8", incompatible); err == nil {
		t.Fatal("calling convention conflict was accepted")
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
}

func TestGenericSymbolImplMethodOwner(t *testing.T) {
	owner := &semantic.StaticImpl{InterfaceName: "A::Protocol", Receiver: &semantic.StructType{Name: "Item", Namespace: "A"}}
	first, err := genericImplSpecializationBase("same_old_spelling", owner, "read")
	if err != nil {
		t.Fatal(err)
	}
	second, err := genericImplSpecializationBase("same_old_spelling", owner, "_read")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("source method identity was lost")
	}
	other := *owner
	other.InterfaceName = "A_Protocol"
	third, err := genericImplSpecializationBase("same_old_spelling", &other, "read")
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("qualified impl identity was sanitized")
	}
}

// Internal names are a versioned implementation detail. Layout/call tests use
// the selected semantic arguments to find the corresponding definition, while
// identity/native controls above independently test separation and consistency.
func genericIRSymbolForTest(t testing.TB, base string, args ...semantic.Type) string {
	t.Helper()
	g := &llvmGenerator{}
	name, err := g.mangleGenericType(base, args)
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Quote(name)
}

func genericIRLayoutNameForTest(t testing.TB, base string, args ...semantic.Type) string {
	t.Helper()
	g := &llvmGenerator{}
	name, err := g.mangleGenericLayoutType(base, args)
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Quote(name)
}
