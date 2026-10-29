//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

const bytePackLoadIR = `target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "aarch64-unknown-linux-gnu"

define i64 @pack(ptr %p) {
entry:
  %b0 = load i8, ptr %p, align 1
  %z0 = zext i8 %b0 to i64
  %p1 = getelementptr i8, ptr %p, i64 1
  %b1 = load i8, ptr %p1, align 1
  %z1 = zext i8 %b1 to i64
  %s1 = shl i64 %z1, 8
  %o1 = or i64 %z0, %s1
  %p2 = getelementptr i8, ptr %p, i64 2
  %b2 = load i8, ptr %p2, align 1
  %z2 = zext i8 %b2 to i64
  %s2 = shl i64 %z2, 16
  %o2 = or i64 %o1, %s2
  %p3 = getelementptr i8, ptr %p, i64 3
  %b3 = load i8, ptr %p3, align 1
  %z3 = zext i8 %b3 to i64
  %s3 = shl i64 %z3, 24
  %o3 = or i64 %o2, %s3
  %p4 = getelementptr i8, ptr %p, i64 4
  %b4 = load i8, ptr %p4, align 1
  %z4 = zext i8 %b4 to i64
  %s4 = shl i64 %z4, 32
  %o4 = or i64 %o3, %s4
  %p5 = getelementptr i8, ptr %p, i64 5
  %b5 = load i8, ptr %p5, align 1
  %z5 = zext i8 %b5 to i64
  %s5 = shl i64 %z5, 40
  %o5 = or i64 %o4, %s5
  %p6 = getelementptr i8, ptr %p, i64 6
  %b6 = load i8, ptr %p6, align 1
  %z6 = zext i8 %b6 to i64
  %s6 = shl i64 %z6, 48
  %o6 = or i64 %o5, %s6
  %p7 = getelementptr i8, ptr %p, i64 7
  %b7 = load i8, ptr %p7, align 1
  %z7 = zext i8 %b7 to i64
  %s7 = shl i64 %z7, 56
  %o7 = or i64 %o6, %s7
  ret i64 %o7
}`

func TestFoldLittleEndianBytePacks(t *testing.T) {
	g, err := parseLLVMIRForSplitHalfTest(bytePackLoadIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if got := foldLittleEndianBytePacks(g.module); got != 1 {
		t.Fatalf("folded %d byte packs, want 1", got)
	}
	if err := g.verify(); err != nil {
		t.Fatalf("folded module failed verification: %v", err)
	}
	ir := g.printModule()
	if !strings.Contains(ir, "load i64, ptr %p, align 1") ||
		!strings.Contains(ir, "ret i64 %elisa.bytepack.load") {
		t.Fatalf("byte pack was not replaced by an alignment-1 i64 load:\n%s", ir)
	}
}

func TestFoldLittleEndianBytePacksDeclinesUnsafeShapes(t *testing.T) {
	cases := map[string]string{
		"big endian": strings.Replace(bytePackLoadIR,
			`target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"`,
			`target datalayout = "E-p:64:64-i64:64-n8:16:32:64-S128"`, 1),
		"noncontiguous": strings.Replace(bytePackLoadIR,
			"getelementptr i8, ptr %p, i64 7", "getelementptr i8, ptr %p, i64 8", 1),
		"intervening write": strings.Replace(bytePackLoadIR,
			"  %p1 = getelementptr", "  store i8 0, ptr %p, align 1\n  %p1 = getelementptr", 1),
		"atomic byte": strings.Replace(bytePackLoadIR,
			"%b0 = load i8, ptr %p, align 1", "%b0 = load atomic i8, ptr %p unordered, align 1", 1),
	}
	for name, ir := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if got := foldLittleEndianBytePacks(g.module); got != 0 {
				t.Fatalf("folded unsafe byte pack %d times, want 0", got)
			}
			if err := g.verify(); err != nil {
				t.Fatalf("unchanged module failed verification: %v", err)
			}
		})
	}
}
