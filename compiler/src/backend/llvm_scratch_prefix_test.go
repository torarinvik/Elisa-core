//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

const scratchPrefixIR = `target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "aarch64-unknown-linux-gnu"
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
define i8 @test(i64 %index) {
entry:
 %buffer = alloca [512 x i8]
 call void @llvm.memset.p0.i64(ptr %buffer, i8 0, i64 512, i1 false)
 %room = icmp ult i64 0, 512
 br i1 %room, label %append, label %exit
append:
 %dest = getelementptr [512 x i8], ptr %buffer, i64 0, i64 0
 store i8 42, ptr %dest
 %next = add i64 0, 1
 %within = icmp ult i64 %index, %next
 br i1 %within, label %capacity, label %exit
capacity:
 %bounded = icmp ult i64 %index, 512
 br i1 %bounded, label %read, label %exit
read:
 %source = getelementptr [512 x i8], ptr %buffer, i64 0, i64 %index
 %value = load i8, ptr %source
 ret i8 %value
exit:
 ret i8 0
}
`

func TestScratchPrefixElisionAndSafetyControls(t *testing.T) {
	if !scratchPrefixSupported() {
		t.Skip("requires LLVM 23 or newer")
	}
	cases := []struct {
		name, ir string
		removed  uint
	}{
		{"append then bounded read", scratchPrefixIR, 1},
		{"gap", strings.Replace(scratchPrefixIR, "%next = add i64 0, 1", "%next = add i64 0, 2", 1), 0},
		{"missing store", strings.Replace(scratchPrefixIR, "store i8 42, ptr %dest", "", 1), 0},
		{"inclusive read", strings.Replace(scratchPrefixIR, "icmp ult i64 %index, %next", "icmp ule i64 %index, %next", 1), 0},
		{"wide read", strings.Replace(scratchPrefixIR, "%value = load i8, ptr %source", "%wide = load i16, ptr %source\n %value = trunc i16 %wide to i8", 1), 0},
		{"volatile read", strings.Replace(scratchPrefixIR, "load i8", "load volatile i8", 1), 0},
		{"volatile fill", strings.Replace(scratchPrefixIR, "i1 false)", "i1 true)", 1), 0},
		{"nonzero fill", strings.Replace(scratchPrefixIR, "i8 0, i64 512", "i8 1, i64 512", 1), 0},
		{"partial fill", strings.Replace(scratchPrefixIR, "i8 0, i64 512", "i8 0, i64 511", 1), 0},
		{"debug module", scratchPrefixIR + "\n!llvm.dbg.cu = !{}\n!llvm.module.flags = !{!0}\n!0 = !{i32 2, !\"Debug Info Version\", i32 3}\n", 0},
		{"pointer escape", strings.Replace(scratchPrefixIR, "%room = icmp", "call void @escape(ptr %buffer)\n %room = icmp", 1) + "\ndeclare void @escape(ptr)\n", 0},
		{"sanitized", strings.Replace(scratchPrefixIR, "@test(i64 %index) {", "@test(i64 %index) sanitize_address {", 1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			before := g.printModule()
			if n := elideScratchPrefixInitializers(g.module); uint(n) != tc.removed {
				t.Fatalf("removed %d initializers, want %d", n, tc.removed)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			after := g.printModule()
			if tc.removed == 0 && before != after {
				t.Fatal("rejected intrinsic input was modified")
			}
			if tc.removed != 0 {
				if n := elideScratchPrefixInitializers(g.module); n != 0 {
					t.Fatalf("second pass removed %d", n)
				}
				if g.printModule() != after {
					t.Fatal("second pass changed module")
				}
			}
		})
	}
}
