//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRKeepsFullyMangledWasmIntrinsicName(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "wasm_memory_intrinsic.elisa", `
@intrinsic("llvm.wasm.memory.grow.i32")
extern wasm_memory_grow(memory_index: u32, pages: u32) -> i32

def grow_one() -> i32:
	return wasm_memory_grow(0, 1)
`)

	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()

	if !strings.Contains(output, "declare i32 @llvm.wasm.memory.grow.i32(i32, i32)") {
		t.Fatalf("expected the fully mangled WASM intrinsic declaration, got IR:\n%s", output)
	}
	if strings.Contains(output, "llvm.wasm.memory.grow.i32.i32") {
		t.Fatalf("fully mangled WASM intrinsic was mangled a second time, got IR:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesAnnotatedComponentExportLinkName(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "wasm_component_export.elisa", `
def local_impl() -> void:
	pass

@link_name("example:guest#start")
export fn start() -> void = local_impl
`)

	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()

	if !strings.Contains(output, `define void @"example:guest#start"()`) {
		t.Fatalf("expected the annotated component export symbol, got IR:\n%s", output)
	}
}
