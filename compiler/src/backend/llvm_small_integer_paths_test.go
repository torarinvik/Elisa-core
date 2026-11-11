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
		cast string
	}{
		{"small pure domain", source, true, ""},
		{"biased low tail", strings.Replace(source, "  %valid = icmp ult i64 %input, 16",
			"  %key = add i64 %input, -10\n  %valid = icmp ult i64 %key, 16", 1), true, ""},
		{"biased high tail", strings.Replace(source, "  %valid = icmp ult i64 %input, 16",
			"  %key = add i64 %input, -26\n  %valid = icmp uge i64 %key, -16", 1), true, ""},
		{"signed guard", strings.Replace(source, "icmp ult i64 %input", "icmp slt i64 %input", 1), false, ""},
		{"large domain", strings.Replace(source, "%input, 16", "%input, 129", 1), false, ""},
		{"observable operation", strings.Replace(source, "  %next = add", "  call void @observe(i64 %updated)\n  %next = add", 1), false, ""},
		{"narrow unsigned results", strings.Replace(strings.Replace(source, "mul i64 %product", "add i64 %product", 1), "%input, 16", "%input, 22", 1), true, "zext i8"},
		{"narrow signed results", strings.Replace(source, "mul i64 %product", "sub i64 %product", 1), true, "sext i8"},
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
			ir := g.printModule()
			if strings.Contains(ir, "@integer.path.results") != test.fold {
				t.Fatalf("unexpected constant path table:\n%s", ir)
			}
			if test.cast != "" && !strings.Contains(ir, test.cast) {
				t.Fatalf("expected %s after narrow table load:\n%s", test.cast, ir)
			}
		})
	}
}
