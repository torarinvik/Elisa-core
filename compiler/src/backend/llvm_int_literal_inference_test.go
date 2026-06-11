package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMForUnsuffixedIntegerLiteralsInUnsignedContexts(t *testing.T) {
	src := `
layout soa struct PendingGotoStore:
	name_key: usize
	depth: usize

def build(owner: Arena, items: darray[usize]) -> usize:
	alloc: mutable Arena& = (&owner).cast[mutable Arena&]
	in alloc:
		pending: mutable PendingGotoStore = zeroed
		pending.push(1, 2)
		total: mutable usize = 0
		index: mutable usize = 0
		if items.count > 0:
			total <- total + items[0]
		for row in pending.rows():
			total <- total + row.name_key + row.depth
		index <- index + 1
		chunk: view[usize] = items[0:1]
		total <- total + chunk[0]
		return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_int_literal_unsigned_contexts.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "define i64 @build") {
		t.Fatalf("expected build definition in generated LLVM, got:\n%s", output)
	}
}
