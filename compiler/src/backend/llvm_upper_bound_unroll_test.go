//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const upperBoundUnrollIR = `define i64 @bounded(ptr %a, i64 noundef %n) {
entry:
 %valid = icmp ule i64 %n, 6
 br i1 %valid, label %check, label %invalid
invalid:
 ret i64 -1
check:
 %empty = icmp eq i64 %n, 0
 br i1 %empty, label %empty.exit, label %loop
empty.exit:
 ret i64 0
loop:
 %i = phi i64 [0, %check], [%next, %loop]
 %sum = phi i64 [0, %check], [%total, %loop]
 %p = getelementptr i64, ptr %a, i64 %i
 %v = load i64, ptr %p, align 8
 %updated = add i64 %v, 1
 store i64 %updated, ptr %p, align 8
 %total = add i64 %sum, %v
 %next = add nuw nsw i64 %i, 1
 %done = icmp eq i64 %next, %n
 br i1 %done, label %exit, label %loop
exit:
 ret i64 %total
}
define i64 @exact(ptr %a) {
entry:
 br label %loop
loop:
 %i = phi i64 [0, %entry], [%next, %loop]
 %sum = phi i64 [0, %entry], [%total, %loop]
 %p = getelementptr i64, ptr %a, i64 %i
 %v = load i64, ptr %p, align 8
 %updated = add i64 %v, 1
 store i64 %updated, ptr %p, align 8
 %total = add i64 %sum, %v
 %next = add nuw nsw i64 %i, 1
 %done = icmp eq i64 %next, 6
 br i1 %done, label %exit, label %loop
exit:
 ret i64 %total
}
`

func TestUpperBoundUnrollRetainsExactTripOptimization(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("qualified AArch64 policy")
	}
	cc, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	g, err := parseLLVMIRForSplitHalfTest(upperBoundUnrollIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	if err := g.optimizeModule(OptimizationLevel3); err != nil {
		t.Fatal(err)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	exact := splitHalfAddFunctionBody(g.printModule(), "exact")
	if exact == "" || strings.Contains(exact, " phi ") {
		t.Fatalf("constant six-iteration loop retained a recurrence:\n%s", exact)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "unroll.o")
	bin := filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cc, "-O2", filepath.Join("testdata", "upper_bound_unroll.c"), obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
}

func TestUpperBoundUnrollPolicyInCacheKey(t *testing.T) {
	key := optimizedModuleCacheKey(&llvmGenerator{}, OptimizationLevel3, "aarch64-unknown-linux-gnu", "generic", "")
	expected := fmt.Sprintf("\"upper-bound-unroll\":%d", upperBoundUnrollLimit())
	if !strings.Contains(key, expected) {
		t.Fatalf("cache key omits policy %s: %s", expected, key)
	}
}
