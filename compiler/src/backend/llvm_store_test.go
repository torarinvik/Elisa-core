package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersStoreSugar(t *testing.T) {
	src := `store PendingGotoStore:
    name_key: u32
    depth: u32

def build(owner: Arena) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.reserve(8u)
        pending.push(1u32, 2u32)
        pending.push(3u32, 4u32)
        pending.truncate(1u)
        pending.clear()
        return pending.name_key.count + pending.depth.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_store.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "store.name_key.push.slot") {
		t.Fatalf("expected store push lowering for first column, got:\n%s", output)
	}
	if !strings.Contains(output, "store.depth.push.slot") {
		t.Fatalf("expected store push lowering for second column, got:\n%s", output)
	}
	if !strings.Contains(output, "store.name_key.truncate.count") {
		t.Fatalf("expected store truncate lowering for first column, got:\n%s", output)
	}
}
