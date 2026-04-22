package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersReverseIterableScopeAndCheckpoint(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]

def build(owner: Arena, items: darray[int]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
	total: mutable usize = 0
    for value in rev(items):
			total <- total + 1
	scope pool_new(2):
        pass
    in alloc:
        xs: mutable darray[int] = [1, 2, 3]
        checkpoint mark = xs:
            xs.push(4)
        return xs.count + total
`
	result := parseAndAnalyzeBackendTest(t, "backend_scope_checkpoint.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "iter.rev.index") {
		t.Fatalf("expected reverse iterable loop lowering, got:\n%s", output)
	}
	if !strings.Contains(output, "thread_pool_shutdown") && !strings.Contains(output, "pool_shutdown") {
		t.Fatalf("expected scope guard cleanup to lower to pool shutdown, got:\n%s", output)
	}
	if !strings.Contains(output, ".checkpoint.count") {
		t.Fatalf("expected darray checkpoint lowering to snapshot the count field, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersGroupedCheckpoint(t *testing.T) {
	src := `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[int] = [1, 2]
        ys: mutable darray[int] = [3, 4]
        checkpoint xs, ys:
            xs.push(5)
            ys.push(6)
        return xs.count + ys.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_grouped_scope_checkpoint.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if count := strings.Count(output, ".checkpoint.count"); count < 2 {
		t.Fatalf("expected grouped checkpoint lowering to snapshot both darray counts, got %d occurrences:\n%s", count, output)
	}
}
