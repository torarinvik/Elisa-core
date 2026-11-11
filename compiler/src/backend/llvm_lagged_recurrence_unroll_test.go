//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

func TestScaledLaggedRecurrencePeelsAndUnrolls(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @bounded_recurrence(i64 %limit) {
entry:
  %invalid = icmp ugt i64 %limit, 50
  br i1 %invalid, label %exit, label %valid
valid:
  %short = icmp ult i64 %limit, 2
  br i1 %short, label %exit, label %loop
loop:
  %previous = phi i64 [ 0, %valid ], [ %current, %loop ]
  %current = phi i64 [ 1, %valid ], [ %next, %loop ]
  %counter = phi i64 [ 2, %valid ], [ %step, %loop ]
  %scale = shl i64 %current, 1
  %next = add i64 %scale, %previous
  %step = add nuw nsw i64 %counter, 1
  %done = icmp eq i64 %counter, %limit
  br i1 %done, label %exit, label %loop
exit:
  %result = phi i64 [ -1, %entry ], [ %limit, %valid ], [ %next, %loop ]
  ret i64 %result
}
`
	g, err := parseLLVMIRForSplitHalfTest(source)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	if got := unrollBoundedDivisionLoops(g); got != 1 {
		t.Fatalf("unroll returned %d, want changed", got)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	ir := g.printModule()
	if !strings.Contains(ir, "icmp eq i64 %limit, 2") || !strings.Contains(ir, "add i64 %niter, 4") {
		t.Fatalf("missing peeled exit or four-step body:\n%s", ir)
	}
}
