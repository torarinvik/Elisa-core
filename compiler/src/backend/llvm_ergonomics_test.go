package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersStoreRowDestructuring(t *testing.T) {
	src := `store PendingGotoStore:
    name_key: usize
    depth: usize

def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.push(1, 2)
        pending.push(3, 4)
        total: mutable usize = 0
        for row in pending.rows():
            let {name_key, depth} = row
            total <- total + name_key + depth
        for {name_key, depth} in pending.rows():
            total <- total + name_key + depth
        return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_store_row_destructure.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, want := range []string{"let.destructure.", "iter.destructure.row.", "name_key.row.index", "depth.row.index"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected store-row destructuring lowering to include %q, got:\n%s", want, output)
		}
	}
}

func TestGenerateLLVMIRLowersWithArenaScopedAllocatorShorthand(t *testing.T) {
	src := `def build() -> usize:
    can Memory.Allocate:
        with arena scratch(4096) as owner:
            xs: darray[int] = [1, 2, 3]
            return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_with_arena_scoped_allocator.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, want := range []string{"@new_region", "@arena_alloc", "darray.literal.alloc"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected scoped arena shorthand lowering to include %q, got:\n%s", want, output)
		}
	}
}
