//go:build cgo

package backend

import (
	"path/filepath"
	"strings"
	"testing"
)

// A literal edit in left must not invalidate right's unrelated shard.
func TestIncrementalUnnamedConstantEditLocality(t *testing.T) {
	base := `extern atoi(value: cstr) -> int

def left(value: int) -> int:
    trusted Unsafe.RawExtern:
        return atoi("41") + value

def right(value: int) -> int:
    return value + 1

def main() -> int:
    return left(0) + right(0)
`
	edited := strings.Replace(base, `"41"`, `"42"`, 1)
	session := NewIncrementalObjectSession(2, 1<<20)
	session.minWeight = 0
	object := filepath.Join(t.TempDir(), "main.o")
	for revision, source := range []string{base, base, edited, base} {
		result := parseAndAnalyzeBackendTest(t, "constant_fanout.elisa", source)
		ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
		if err != nil {
			t.Fatal(err)
		}
		literal := `c"41\00"`
		if revision == 2 {
			literal = `c"42\00"`
		}
		retained := false
		for _, line := range strings.Split(ir, "\n") {
			if strings.Contains(line, "private unnamed_addr constant") && strings.Contains(line, literal) {
				retained = true
			}
		}
		if !retained {
			t.Fatalf("fixture did not retain address-insignificant literal %s in LLVM IR", literal)
		}
		before := session.Stats()
		if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
			t.Fatal(err)
		}
		after := session.Stats()
		if revision == 0 {
			assertNativeFunctionsInDifferentShards(t, session, "left", "right")
		}
		wantStatus := 42
		if revision == 2 {
			wantStatus = 43
		}
		if got := incrementalObjectNativeStatus(t, object); got != wantStatus {
			t.Fatalf("revision %d native status=%d, want %d", revision, got, wantStatus)
		}
		fresh := filepath.Join(t.TempDir(), "fresh.o")
		if err := WriteLLVMObjectFileWithOptions(result, fresh, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
			t.Fatal(err)
		}
		if got := incrementalObjectNativeStatus(t, fresh); got != wantStatus {
			t.Fatalf("revision %d fresh native status=%d, want %d", revision, got, wantStatus)
		}
		if after.RetainedBytes > session.maxBytes {
			t.Fatalf("retained bytes exceed budget: %+v", after)
		}
		wantMiss := uint64(0)
		if revision == 0 {
			wantMiss = 2
		}
		if revision == 2 {
			wantMiss = 1
		}
		if got := after.PartitionsEmitted - before.PartitionsEmitted; got != wantMiss {
			t.Fatalf("revision %d emitted=%d reused=%d, want %d misses; unrelated constant bytes invalidated shard", revision, got, after.PartitionsReused-before.PartitionsReused, wantMiss)
		}
		if got := after.PartitionsReused - before.PartitionsReused; got != 2-wantMiss {
			t.Fatalf("revision %d reused=%d, want %d", revision, got, 2-wantMiss)
		}
		if after.SerialFallbacks != 0 {
			t.Fatal("fixture fell back to serial emission")
		}
	}
}
