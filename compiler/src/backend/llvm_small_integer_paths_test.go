//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

func TestConstantPathTableRequiresBoundedPureIntegerPaths(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
declare void @observe(i64)
define i64 @bounded_product(i64 %input) {
entry:
  %valid = icmp ult i64 %input, 16
  br i1 %valid, label %loop, label %invalid
loop:
  %index = phi i64 [ 1, %entry ], [ %next, %loop ]
  %product = phi i64 [ 1, %entry ], [ %updated, %loop ]
  %updated = mul i64 %product, %index
  %next = add i64 %index, 1
  %done = icmp uge i64 %index, %input
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %updated
invalid:
  ret i64 -1
}
`
	for _, test := range []struct {
		name string
		ir   string
		fold bool
	}{
		{"small pure domain", source, true},
		{"signed guard", strings.Replace(source, "icmp ult i64 %input", "icmp slt i64 %input", 1), false},
		{"large domain", strings.Replace(source, "%input, 16", "%input, 65", 1), false},
		{"observable operation", strings.Replace(source, "  %next = add", "  call void @observe(i64 %updated)\n  %next = add", 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(test.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if err := g.ensureTargetMachine(); err != nil {
				t.Fatal(err)
			}
			if unrollBoundedDivisionLoops(g) == 2 {
				t.Fatal("LLVM cleanup failed")
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if ir := g.printModule(); strings.Contains(ir, "@integer.path.results") != test.fold {
				t.Fatalf("unexpected constant path table:\n%s", ir)
			}
		})
	}
}
