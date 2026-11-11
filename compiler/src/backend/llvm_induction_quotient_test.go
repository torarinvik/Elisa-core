//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

func TestInductionQuotientRequiresSignAndBackedgeProof(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @first_divisor(i64 %input) {
entry:
  %n = freeze i64 %input
  %small = icmp samesign ult i64 %n, 4
  br i1 %small, label %exit, label %loop
loop:
  %cursor = phi i64 [ 2, %entry ], [ %next, %latch ]
  %quotient = sdiv i64 %n, %cursor
  %product = mul i64 %quotient, %cursor
  %divides = icmp eq i64 %n, %product
  br i1 %divides, label %found, label %latch
latch:
  %next = add i64 %cursor, 1
  %next.quotient = udiv i64 %n, %next
  %done = icmp sgt i64 %next, %next.quotient
  br i1 %done, label %exit, label %loop
found:
  ret i64 %cursor
exit:
  ret i64 0
}
`
	for _, test := range []struct {
		name string
		ir   string
		fold bool
	}{
		{"matching loop", source, true},
		{"missing entry sign", strings.Replace(source, "icmp samesign ult", "icmp ult", 1), false},
		{"unbounded backedge", strings.Replace(source, "icmp sgt", "icmp slt", 1), false},
		{"different latch divisor", strings.Replace(source, "udiv i64 %n, %next", "udiv i64 %n, %cursor", 1), false},
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
			if ir := g.printModule(); strings.Contains(ir, "%induction.quotient = phi") != test.fold {
				t.Fatalf("unexpected induction quotient reuse:\n%s", ir)
			}
		})
	}
}
