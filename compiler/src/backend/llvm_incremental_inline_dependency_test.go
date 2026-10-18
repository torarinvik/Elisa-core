//go:build cgo

package backend

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestIncrementalObjectInlineCalleeInvalidatesUntouchedCaller(t *testing.T) {
	session := NewIncrementalObjectSession(4, 1<<20)
	session.minWeight = 0
	dir := t.TempDir()
	for revision, offset := range []int{1, 1, 2, 1} {
		source := fmt.Sprintf(`@callconv(c)
@inline(always)
def inline_leaf(value: i64) -> i64:
    return value + %d

@callconv(c)
@inline(never)
def untouched_caller(value: i64) -> i64:
    return inline_leaf(value)

export fn inline_leaf(value: i64) -> i64 = inline_leaf
export fn untouched_caller(value: i64) -> i64 = untouched_caller

def main() -> i64:
    return untouched_caller(40)

`, offset)
		for i := 0; i < 16; i++ {
			source += fmt.Sprintf("def padding%d(value: i64) -> i64:\n    return value + %d\n\n", i, i)
		}
		result := parseAndAnalyzeBackendTest(t, "inline_dependency.elisa", source)
		ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
		if err != nil {
			t.Fatal(err)
		}
		location := regexp.MustCompile(`(?m)^define [^\n]*@untouched_caller\(`).FindStringIndex(ir)
		if location == nil {
			t.Fatal("missing caller definition")
		}
		tail := ir[location[0]:]
		end := strings.Index(tail, "\n}")
		if end < 0 {
			t.Fatal("unterminated caller definition")
		}
		if strings.Contains(tail[:end], "@inline_leaf") {
			t.Fatal("fixture did not inline callee into caller")
		}
		options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}
		before := session.Stats()
		retained := filepath.Join(dir, "retained.o")
		if err := session.WriteObject(result, retained, options); err != nil {
			t.Fatal(err)
		}
		after := session.Stats()
		emitted, reused := after.PartitionsEmitted-before.PartitionsEmitted, after.PartitionsReused-before.PartitionsReused
		if revision == 0 {
			assertNativeFunctionsInDifferentShards(t, session, "inline_leaf", "untouched_caller")
		}
		if revision == 1 || revision == 3 {
			if emitted != 0 || reused != 4 {
				t.Fatalf("revision%d failed exact reuse: emitted=%d reused=%d", revision, emitted, reused)
			}
		}
		if revision == 2 && (emitted < 2 || reused == 0) {
			t.Fatalf("callee edit must invalidate caller's different shard: emitted=%d reused=%d", emitted, reused)
		}
		if after.SerialFallbacks != 0 || after.RetainedBytes > session.maxBytes {
			t.Fatalf("unexpected fallback or retention overflow: %+v", after)
		}
		fresh := filepath.Join(dir, "fresh.o")
		if err := WriteLLVMObjectFileWithOptions(result, fresh, options); err != nil {
			t.Fatal(err)
		}
		for _, object := range []string{retained, fresh} {
			if got := incrementalObjectNativeStatus(t, object); got != 40+offset {
				t.Fatalf("revision%d object=%s status=%d want=%d", revision, object, got, 40+offset)
			}
		}
	}
}
