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
		{"boolean results in a register", strings.Replace(source, "ret i64 %updated",
			"%bit = and i64 %updated, 1\n  ret i64 %bit", 1), false, "lshr i64"},
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
				t.Fatalf("expected %s in the folded lookup:\n%s", test.cast, ir)
			}
		})
	}
}

func TestZeroProductBypassRequiresEquivalentReturn(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @zero_product(i64 %key, i64 %other) {
guard:
  %is_zero = icmp eq i64 %key, 0
  br i1 %is_zero, label %join, label %product
product:
  %square = mul nuw nsw i64 %key, %key
  %cube = mul nuw nsw i64 %square, %key
  br label %join
join:
  %result = phi i64 [ 0, %guard ], [ %cube, %product ]
  ret i64 %result
}
`
	for _, test := range []struct {
		name string
		ir   string
		fold bool
	}{
		{"zero bypass", source, true},
		{"different bypass value", strings.Replace(source, "[ 0, %guard ]", "[ 1, %guard ]", 1), false},
		{"unknown multiplier", strings.Replace(source, "%square, %key", "%square, %other", 1), false},
		{"poison multiplier", strings.Replace(source, "%square, %key", "%square, poison", 1), false},
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
			if strings.Contains(ir, "br i1 %is_zero") == test.fold {
				t.Fatalf("unexpected zero branch:\n%s", ir)
			}
		})
	}
}

func TestLiteralDivisorSpecializationRequiresDirectConstantCalls(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define internal i32 @digits(i32 %n, i32 %base) {
entry:
  %safe = freeze i32 %base
  %q1 = udiv i32 %n, %safe
  %r1 = urem i32 %n, %safe
  %q2 = udiv i32 %q1, %safe
  %r2 = urem i32 %q1, %safe
  %q3 = udiv i32 %q2, %safe
  %r3 = urem i32 %q2, %safe
  %q4 = udiv i32 %q3, %safe
  %r4 = urem i32 %q3, %safe
  %s1 = add i32 %r1, %r2
  %s2 = add i32 %r3, %r4
  %s3 = add i32 %s1, %s2
  %sum = add i32 %s3, %q4
  ret i32 %sum
}

define i32 @two_bases(i32 %value, i32 %runtime_base) {
entry:
  %decimal = call i32 @digits(i32 %value, i32 10)
  %hex = call i32 @digits(i32 %value, i32 16)
  %sum = add i32 %decimal, %hex
  ret i32 %sum
}
`
	for _, test := range []struct {
		name string
		ir   string
		fold bool
	}{
		{"distinct literals", source, true},
		{"runtime divisor", strings.Replace(source, "%value, i32 16)", "%value, i32 %runtime_base)", 1), false},
		{"equal literals", strings.Replace(source, "%value, i32 16)", "%value, i32 10)", 1), false},
		{"address taken", source + "@address = global ptr @digits\n", false},
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
			if strings.Contains(ir, "@digits.literal_divisor") != test.fold {
				t.Fatalf("unexpected divisor specialization:\n%s", ir)
			}
			if test.fold && strings.Contains(ir, ", %safe") {
				t.Fatalf("specialized helper still has a runtime divisor:\n%s", ir)
			}
		})
	}
}

func TestBoundedQuotientSumRequiresUpperBoundAndZeroSeed(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @sum_quotients(i64 %input) {
entry:
  %invalid = icmp ugt i64 %input, 1000000000
  br i1 %invalid, label %exit, label %pre
pre:
  %zero = icmp eq i64 %input, 0
  br i1 %zero, label %exit, label %body
body:
  %sum = phi i64 [ 0, %pre ], [ %next_sum, %body ]
  %word = phi i64 [ %input, %pre ], [ %next_word, %body ]
  %next_word = udiv i64 %word, 5
  %next_sum = add i64 %sum, %next_word
  %done = icmp ult i64 %word, 5
  br i1 %done, label %exit, label %body
exit:
  %result = phi i64 [ -1, %entry ], [ 0, %pre ], [ %next_sum, %body ]
  ret i64 %result
}
`
	for _, test := range []struct {
		name string
		ir   string
		fold bool
	}{
		{"bounded", source, true},
		{"wide input", strings.Replace(source, "1000000000", "4294967295", 1), false},
		{"nonzero seed", strings.Replace(source, "%sum = phi i64 [ 0", "%sum = phi i64 [ 1", 1), false},
		{"different exit condition", strings.Replace(source, "icmp ult i64 %word, 5", "icmp ult i64 %word, 6", 1), false},
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
			if ir := g.printModule(); strings.Contains(ir, "quotient.sum.parallel") != test.fold {
				t.Fatalf("unexpected quotient sum version:\n%s", ir)
			}
		})
	}
}
