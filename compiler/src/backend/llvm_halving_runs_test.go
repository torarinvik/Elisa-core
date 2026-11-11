//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

func TestHalvingRunsRequiresParityAndUnobservedIntermediateState(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @count(i64 %input) {
entry:
  br label %loop
loop:
  %steps = phi i64 [ 0, %entry ], [ %step, %loop ]
  %word = phi i64 [ %input, %entry ], [ %next, %loop ]
  %half = lshr i64 %word, 1
  %bit = and i64 %word, 1
  %even = icmp eq i64 %bit, 0
  %triple = mul i64 %word, 3
  %odd = add i64 %triple, 1
  %next = select i1 %even, i64 %half, i64 %odd
  %step = add i64 %steps, 1
  %done = icmp eq i64 %next, 1
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %step
}
`
	for _, test := range []struct {
		name string
		ir   string
		fold bool
	}{
		{"counted recurrence", source, true},
		{"intermediate word escapes", strings.Replace(source, "ret i64 %step", "ret i64 %word", 1), false},
		{"different parity condition", strings.Replace(source, "icmp eq i64 %bit, 0", "icmp eq i64 %bit, 2", 1), false},
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
			if ir := g.printModule(); strings.Contains(ir, "@llvm.cttz.i64") != test.fold {
				t.Fatalf("unexpected grouped halving lowering:\n%s", ir)
			}
		})
	}
}
