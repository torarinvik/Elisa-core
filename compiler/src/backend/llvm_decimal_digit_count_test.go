//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

func TestDecimalDigitCountRequiresUnobservedDivideByTenRecurrence(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @digits(i64 noundef %input) {
entry:
  br label %loop
loop:
  %word = phi i64 [ %input, %entry ], [ %quotient, %loop ]
  %count = phi i64 [ 0, %entry ], [ %next, %loop ]
  %quotient = udiv i64 %word, 10
  %next = add i64 %count, 1
  %done = icmp ult i64 %word, 10
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %next
}
`
	for _, test := range []struct {
		name string
		ir   string
		fold bool
	}{
		{"decimal count", source, true},
		{"quotient is observed", strings.Replace(source, "ret i64 %next", "ret i64 %quotient", 1), false},
		{"different radix", strings.ReplaceAll(source, "%word, 10", "%word, 8"), false},
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
			if ir := g.printModule(); strings.Contains(ir, "@decimal.digit.powers") != test.fold {
				t.Fatalf("unexpected decimal digit count lowering:\n%s", ir)
			}
		})
	}
}

func TestPowerOfTwoDigitCountPreservesObservedLoopValues(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @digits(i64 noundef %input) {
entry:
  %multi = icmp ugt i64 %input, 7
  br i1 %multi, label %loop, label %exit
loop:
  %word = phi i64 [ %input, %entry ], [ %quotient, %loop ]
  %count = phi i64 [ 1, %entry ], [ %next, %loop ]
  %quotient = lshr i64 %word, 3
  %next = add i64 %count, 1
  %again = icmp ugt i64 %word, 63
  br i1 %again, label %loop, label %exit
exit:
  %width = phi i64 [ 1, %entry ], [ %next, %loop ]
  ret i64 %width
}
`
	for _, test := range []struct {
		name string
		ir   string
		fold bool
	}{
		{"octal count", source, true},
		{"wrong loop threshold", strings.Replace(source, "%word, 63", "%word, 64", 1), false},
		{"observed quotient", strings.Replace(source, "[ %next, %loop ]\n  ret", "[ %quotient, %loop ]\n  ret", 1), false},
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
			if ir := g.printModule(); strings.Contains(ir, "@llvm.ctlz.i64") != test.fold {
				t.Fatalf("unexpected power-of-two digit count lowering:\n%s", ir)
			}
		})
	}
}
