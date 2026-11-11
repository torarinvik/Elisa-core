//go:build cgo && (darwin || linux)

package backend

import (
	"runtime"
	"strings"
	"testing"
)

const trailingZeroLoopIR = `target triple = "aarch64-apple-macosx15.0.0"
define i64 @probe(i64 %input) {
entry:
  %nonzero = icmp ne i64 %input, 0
  br i1 %nonzero, label %loop, label %zero
loop:
  %word = phi i64 [ %input, %entry ], [ %shift, %loop ]
  %count = phi i64 [ 0, %entry ], [ %increment, %loop ]
  %shift = lshr i64 %word, 1
  %increment = add i64 %count, 1
  %bit = and i64 %word, 1
  %even = icmp eq i64 %bit, 0
  br i1 %even, label %loop, label %done
zero:
  ret i64 0
done:
  %result = add i64 %word, %count
  ret i64 %result
}
`

func TestFoldTrailingZeroLoops(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("qualified AArch64 targets")
	}
	post := strings.NewReplacer(
		"lshr i64", "lshr exact i64",
		"and i64 %word, 1", "and i64 %word, 2",
		"add i64 %word, %count", "add i64 %word, %increment",
	).Replace(trailingZeroLoopIR)
	cases := []struct {
		name string
		ir   string
		want bool
	}{
		{"pre", trailingZeroLoopIR, true},
		{"post", post, true},
		{"zero can enter", strings.Replace(trailingZeroLoopIR, "br i1 %nonzero, label %loop, label %zero", "br label %loop", 1), false},
		{"post shift is not exact", strings.Replace(post, "lshr exact", "lshr", 1), false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(test.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if unrollBoundedDivisionLoops(g) == 2 {
				t.Fatal("LLVM cleanup failed")
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if ir := g.printModule(); strings.Contains(ir, "@llvm.cttz") != test.want {
				t.Fatalf("trailing-zero match = %v, want %v\n%s", !test.want, test.want, ir)
			}
		})
	}
}
