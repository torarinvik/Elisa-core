//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

func TestFactorRemovalReusesOnlyTheMatchingEntryQuotient(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @remove_factor(i64 %n, i1 %two) {
entry:
  br i1 %two, label %even, label %odd
even:
  %by_two = lshr i64 %n, 1
  br label %guard
odd:
  %by_three = udiv i64 %n, 3
  br label %guard
guard:
  %factor = phi i64 [ 2, %even ], [ 3, %odd ]
  %carried = phi i64 [ %by_two, %even ], [ %by_three, %odd ]
  %product = mul i64 %factor, %carried
  %divides = icmp eq i64 %n, %product
  br i1 %divides, label %loop, label %exit
loop:
  %remaining = phi i64 [ %n, %guard ], [ %quotient, %loop ]
  %quotient = udiv i64 %remaining, %factor
  %multiple = mul nuw i64 %quotient, %factor
  %again = icmp eq i64 %remaining, %multiple
  br i1 %again, label %loop, label %exit
exit:
  %answer = phi i64 [ %n, %guard ], [ %remaining, %loop ]
  ret i64 %answer
}
`
	for _, test := range []struct {
		name      string
		ir        string
		wantReuse bool
	}{
		{"matching", source, true},
		{"wrong shift", strings.Replace(source, "lshr i64 %n, 1", "lshr i64 %n, 2", 1), false},
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
			if status := unrollBoundedDivisionLoops(g); status == 2 {
				t.Fatal("division cleanup failed")
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			ir := g.printModule()
			if got := strings.Contains(ir, "[ %carried, %guard ]"); got != test.wantReuse {
				t.Fatalf("entry quotient reuse=%t, want %t:\n%s", got, test.wantReuse, ir)
			}
		})
	}
}
