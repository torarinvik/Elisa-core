//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const privateInitSinkIR = `target triple = "aarch64-apple-macosx15.0.0"
define i64 @probe(i64 %n) {
entry:
  %scratch = alloca [512 x i8], align 16
  %base = getelementptr [512 x i8], ptr %scratch, i64 0, i64 0
  call void @llvm.memset.p0.i64(ptr align 16 %base, i8 0, i64 512, i1 false)
  %empty = icmp eq i64 %n, 0
  br i1 %empty, label %exit, label %read
exit:
  %answer = phi i64 [ 7, %entry ], [ %value, %read ]
  ret i64 %answer
read:
  %index = and i64 %n, 511
  %address = getelementptr i8, ptr %base, i64 %index
  %byte = load i8, ptr %address, align 1
  %wide = zext i8 %byte to i64
  %value = add i64 %wide, 13
  br label %exit
}
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1)

define i64 @volatile_probe(i64 %n) {
entry:
  %scratch = alloca [512 x i8], align 16
  %base = getelementptr [512 x i8], ptr %scratch, i64 0, i64 0
  call void @llvm.memset.p0.i64(ptr align 16 %base, i8 0, i64 512, i1 true)
  %empty = icmp eq i64 %n, 0
  br i1 %empty, label %exit, label %read
exit:
  ret i64 7
read:
  %index = and i64 %n, 511
  %address = getelementptr i8, ptr %base, i64 %index
  %byte = load i8, ptr %address, align 1
  %wide = zext i8 %byte to i64
  ret i64 %wide
}
`

func TestSinkPrivateInitializationPreservesEarlyReturnAndPHIs(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("qualified AArch64 targets")
	}
	cc, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	g, err := parseLLVMIRForSplitHalfTest(privateInitSinkIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if changed := sinkPrivateInitializations(g.module); changed != 1 {
		t.Fatalf("sink changed %d initializations, want only the nonvolatile probe", changed)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	module := g.printModule()
	probe := splitHalfAddFunctionBody(module, "probe")
	if !strings.Contains(probe, "stack.init.nonempty") {
		t.Fatalf("initialization was not moved to the continuing edge:\n%s", probe)
	}
	volatile := splitHalfAddFunctionBody(module, "volatile_probe")
	if strings.Contains(volatile, "stack.init.nonempty") {
		t.Fatalf("volatile initialization was moved:\n%s", volatile)
	}
	dir := t.TempDir()
	obj, bin := filepath.Join(dir, "sink.o"), filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "check.c")
	if err := os.WriteFile(harness, []byte(privateInitSinkHarness), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cc, "-O2", harness, obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
}

const privateInitSinkHarness = `#include <stdint.h>
#include <stdio.h>
extern uint64_t probe(uint64_t);
__attribute__((noinline)) static void poison(void) { volatile unsigned char b[8192]; for (unsigned i=0;i<sizeof b;i++) b[i]=0xa5; }
int main(void) { for (uint64_t n=0;n<4096;n++) { poison(); uint64_t want=n ? 13 : 7; if (probe(n)!=want) { fprintf(stderr,"n=%llu got=%llu want=%llu\\n",(unsigned long long)n,(unsigned long long)probe(n),(unsigned long long)want); return 1; } } return 0; }
`
