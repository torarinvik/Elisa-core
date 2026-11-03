//go:build cgo && (darwin || linux)

package backend

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const privateBitUpdateIR = `target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "aarch64-unknown-linux-gnu"
define i64 @update(ptr %input, ptr %output) nounwind {
entry:
 %array = alloca [4 x i16], align 2
 call void @llvm.memset.p0.i64(ptr %array, i8 0, i64 8, i1 false)
 br label %head
head:
 %i = phi i64 [0, %entry], [%next, %join]
 %p = getelementptr i64, ptr %input, i64 %i
 %bit = load i64, ptr %p
 switch i64 %bit, label %invalid [i64 0, label %join
                                i64 1, label %set]
set:
 %slot = getelementptr [2 x i8], ptr %array, i64 %i
 %old = load i16, ptr %slot, align 2
 %value = or i16 %old, 1
 store i16 %value, ptr %slot, align 2
 br label %join
join:
 %next = add nuw nsw i64 %i, 1
 %done = icmp eq i64 %next, 4
 br i1 %done, label %exit, label %head
exit:
 %result = load i64, ptr %array, align 2
 store i64 %result, ptr %output, align 1
 ret i64 8
invalid:
 ret i64 -1
}
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
`

func privateBitBranchIR() string {
	return strings.Replace(privateBitUpdateIR, ` switch i64 %bit, label %invalid [i64 0, label %join
                                i64 1, label %set]`, ` %isone = icmp eq i64 %bit, 1
 br i1 %isone, label %set, label %join`, 1)
}

func TestPrivateBitUpdateExclusions(t *testing.T) {
	for name, tc := range map[string]struct {
		ir   string
		want int
	}{
		"conditional branch":         {privateBitBranchIR(), 1},
		"conditional branch escaped": {strings.Replace(privateBitBranchIR(), " br label %head", " store ptr %array, ptr @escaped\n br label %head", 1) + "@escaped = global ptr null\n", 0},
		"memory sanitizer":           {strings.Replace(privateBitUpdateIR, "nounwind {", "nounwind sanitize_memory {", 1), 0},
		"address sanitizer":          {strings.Replace(privateBitUpdateIR, "nounwind {", "nounwind sanitize_address {", 1), 0},
		"thread sanitizer":           {strings.Replace(privateBitUpdateIR, "nounwind {", "nounwind sanitize_thread {", 1), 0},
		"eligible":                   {privateBitUpdateIR, 1},
		"macOS":                      {strings.Replace(privateBitUpdateIR, "aarch64-unknown-linux-gnu", "arm64-apple-darwin", 1), 1},
		"external array":             {strings.Replace(privateBitUpdateIR, "ptr %array, i64 %i", "ptr %output, i64 %i", 1), 0},
		"out of bounds":              {strings.Replace(privateBitUpdateIR, "alloca [4 x i16]", "alloca [2 x i16]", 1), 0},
		"unknown index":              {strings.Replace(privateBitUpdateIR, "ptr %array, i64 %i", "ptr %array, i64 %bit", 1), 0},
		"under aligned":              {strings.Replace(privateBitUpdateIR, "alloca [4 x i16], align 2", "alloca [4 x i16], align 1", 1), 0},
		"escaped array":              {strings.Replace(privateBitUpdateIR, " br label %head", " store ptr %array, ptr @escaped\n br label %head", 1) + "@escaped = global ptr null\n", 0},
		"volatile load":              {strings.Replace(privateBitUpdateIR, "%old = load i16", "%old = load volatile i16", 1), 0},
		"volatile store":             {strings.Replace(privateBitUpdateIR, "store i16 %value", "store volatile i16 %value", 1), 0},
		"extra effect":               {strings.Replace(privateBitUpdateIR, "set:\n", "set:\n call void @effect()\n", 1) + "declare void @effect()\n", 0},
		"lifetime ended":             {strings.Replace(privateBitUpdateIR, " br label %head", " call void @llvm.lifetime.end.p0(ptr %array)\n br label %head", 1) + "declare void @llvm.lifetime.end.p0(ptr)\n", 0},
		"size optimized":             {strings.Replace(privateBitUpdateIR, "nounwind {", "nounwind optsize {", 1), 0},
		"other target":               {strings.Replace(privateBitUpdateIR, "aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu", 1), 0},
	} {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			if got := predicatePrivateBitUpdates(g.module); got != tc.want {
				t.Fatalf("changed %d, want %d", got, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			after := g.printModule()
			if tc.want == 0 && before != after {
				t.Fatal("changed unsupported input")
			}
			if tc.want == 1 && (!strings.Contains(after, "freeze i16") || strings.Contains(after, "switch i64 %bit")) {
				t.Fatal("missing predication or freeze")
			}
			if predicatePrivateBitUpdates(g.module) != 0 || g.printModule() != after {
				t.Fatal("not idempotent")
			}
		})
	}
}
func TestPrivateBitUpdateRuntime(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64")
	}
	cc, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	for _, kind := range []string{"switch", "branch"} {
		t.Run(kind, func(t *testing.T) {
			ir := privateBitUpdateIR
			flags := []string{}
			if kind == "branch" {
				ir = privateBitBranchIR()
				flags = append(flags, "-DUNVALIDATED")
			}
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if predicatePrivateBitUpdates(g.module) != 1 {
				t.Fatal("not predicated")
			}
			dir := t.TempDir()
			obj := filepath.Join(dir, "update.o")
			bin := filepath.Join(dir, "check")
			if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-O2"}, flags...)
			args = append(args, filepath.Join("testdata", "private_bit_update.c"), obj, "-o", bin)
			if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
				t.Fatalf("%s: %v", out, err)
			}
			if out, err := exec.Command(bin).CombinedOutput(); err != nil {
				t.Fatalf("%s: %v", out, err)
			}
		})
	}
}
