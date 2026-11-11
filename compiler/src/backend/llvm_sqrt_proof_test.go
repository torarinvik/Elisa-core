//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"strings"
	"testing"
)

func TestSqrtNonNaNProof(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	bytes, err := os.ReadFile("testdata/sqrt_proof.ll")
	if err != nil {
		t.Fatal(err)
	}
	fixture := string(bytes)
	cases := map[string]struct {
		ir   string
		want int
	}{
		"squared reduction": {fixture, 1},
		"NaN mean seed":     {strings.Replace(fixture, "phi double [ 0.000000e+00,", "phi double [ 0x7FF8000000000000,", 1), 0},
		"unknown floats":    {strings.ReplaceAll(strings.ReplaceAll(fixture, "sitofp <2 x i64>", "bitcast <2 x i64>"), "sitofp i64", "bitcast i64"), 0},
		"zero denominator":  {strings.Replace(fixture, "%divtmp75.i = fdiv arcp contract double %addtmp68.i.lcssa, %uitofp.i", "%divtmp75.i = fdiv arcp contract double %addtmp68.i.lcssa, 0.000000e+00", 1), 0},
		"strict FP":         {strings.Replace(fixture, "attributes #1 = {", "attributes #1 = { strictfp ", 1), 0},
		"no builtin":        {strings.Replace(fixture, "tail call double @sqrt(double %divtmp75.i)", "tail call double @sqrt(double %divtmp75.i) #3", 1) + "\nattributes #3 = { nobuiltin }\n", 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if got := proveSqrtNonNaN(g); got != tc.want {
				t.Fatalf("changed %d, want %d", got, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if got := proveSqrtNonNaN(g); got != 0 {
				t.Fatalf("second application changed %d calls", got)
			}
		})
	}
}

func TestSqrtNonNaNCleanup(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	bytes, err := os.ReadFile("testdata/sqrt_proof.ll")
	if err != nil {
		t.Fatal(err)
	}
	g, err := parseLLVMIRForSplitHalfTest(string(bytes))
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	if got := optimizeSqrtNonNaN(g); got != 1 {
		t.Fatalf("cleanup status %d", got)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.printModule(), "@llvm.sqrt.f64") {
		t.Fatal("proven library call was not simplified")
	}
}

// An NN induction must not acquire nonnegativity or finiteness from itself.
// -Inf + +Inf is NaN, while the +0-seeded recurrence safely reaches +Inf.
func TestSqrtInfinityInduction(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const ir = `
declare double @sqrt(double)
define double @infinite_reduction(i64 %n) {
entry:
 br label %loop
loop:
 %mean = phi double [ 0xFFF0000000000000, %entry ], [ %next, %loop ]
 %i = phi i64 [ 0, %entry ], [ %inc, %loop ]
 %next = fadd double %mean, 0x7FF0000000000000
 %inc = add i64 %i, 1
 %done = icmp eq i64 %inc, %n
 br i1 %done, label %exit, label %loop
exit:
 %dev = fsub double 1.000000e+00, %next
 %square = fmul double %dev, %dev
 %root = call double @sqrt(double %square)
 ret double %root
}
`
	for _, tc := range []struct {
		name, ir string
		want     int
	}{
		{"opposite infinities", ir, 0},
		{"positive infinity", strings.Replace(ir, "0xFFF0000000000000", "0.000000e+00", 1), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if got := proveSqrtNonNaN(g); got != tc.want {
				t.Fatalf("changed %d, want %d", got, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(g.printModule(), "ninf") {
				t.Fatal("infinity was excluded")
			}
		})
	}
}

func TestSqrtDomainFromOrderedBranch(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
declare double @sqrt(double)
define double @root(double %x, i1 %bypass) {
entry:
  br i1 %bypass, label %fallback, label %guard
guard:
  %ok = fcmp ogt double %x, 0.0
  br i1 %ok, label %calculate, label %fallback
calculate:
  %answer = call double @sqrt(double %x)
  ret double %answer
fallback:
  ret double 0.0
}
`
	for _, tc := range []struct {
		name, old, replacement string
		want                   int
	}{
		{"ordered positive", "", "", 1},
		{"unordered admits NaN", "fcmp ogt", "fcmp ugt", 0},
		{"negative values", "fcmp ogt double %x, 0.0", "fcmp ogt double %x, -1.0", 0},
		{"reversed operands", "fcmp ogt double %x, 0.0", "fcmp ole double 0.0, %x", 1},
		{"alternate predecessor", "%bypass, label %fallback", "%bypass, label %calculate", 0},
		{"opposite edge", "%ok, label %calculate, label %fallback", "%ok, label %fallback, label %calculate", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ir := strings.Replace(source, tc.old, tc.replacement, 1)
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if got := proveSqrtNonNaN(g); got != tc.want {
				t.Fatalf("changed %d, want %d", got, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
