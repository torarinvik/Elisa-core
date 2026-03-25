//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRMarksRegionArenaAllocCallNoAlias(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_noalias_region_new.llcontext", `struct Counter:
    value: i64

def build() -> i64 can[Memory.Allocate, Abort.Panic]:
	region scratch(1024u)
	ptr: scratch Counter& = new[scratch] Counter(1)
	return ptr.value
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	if !strings.Contains(output, "call ptr @arena_alloc(") {
		t.Fatalf("expected region-backed allocation to call arena_alloc, got IR:\n%s", output)
	}
	if !strings.Contains(output, "declare noalias ptr @arena_alloc(") {
		t.Fatalf("expected arena_alloc declaration to carry noalias return attribute, got IR:\n%s", output)
	}
}

func TestGenerateLLVMIRMarksAllocPermCallNoAlias(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_noalias_alloc_perm.llcontext", `extern alloc_perm(size: i64) -> heap void& can[Memory.Allocate, Abort.Panic]

def build() -> heap u8& can[Memory.Allocate, Abort.Panic]:
	return alloc_perm(8i64).cast[heap u8&]
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	if !strings.Contains(output, "call ptr @alloc_perm(") {
		t.Fatalf("expected alloc_perm call to be emitted, got IR:\n%s", output)
	}
	if !strings.Contains(output, "declare noalias ptr @alloc_perm(i64)") {
		t.Fatalf("expected alloc_perm declaration to carry noalias return attribute, got IR:\n%s", output)
	}
}