//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

const remainderReductionIR = `target triple = "aarch64-unknown-linux-gnu"
define i64 @reduce(i64 %arg) {
entry:
  br label %loop
loop:
  %n = phi i64 [ %arg, %entry ], [ %q, %loop ]
  %sum = phi i64 [ 0, %entry ], [ %next, %loop ]
  %q = udiv i64 %n, 10
  %neg = mul nsw i64 %q, -10
  %inner = add i64 %n, %sum
  %next = add i64 %inner, %neg
  %done = icmp ult i64 %n, 10
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %next
}`

func TestGroupRemainderReductions(t *testing.T) {
	for _, ir := range []string{remainderReductionIR,
		strings.ReplaceAll(strings.ReplaceAll(remainderReductionIR,
			"%n, %sum", "%sum, %n"), "%inner, %neg", "%neg, %inner"),
		strings.ReplaceAll(remainderReductionIR, "10", "13"),
		strings.Replace(remainderReductionIR, "udiv i64", "udiv exact i64", 1),
		strings.Replace(remainderReductionIR, "mul nsw", "mul nuw", 1),
		strings.ReplaceAll(remainderReductionIR, "10", "9223372036854775808")} {
		g, err := parseLLVMIRForSplitHalfTest(ir)
		if err != nil {
			t.Fatal(err)
		}
		if got := groupRemainderReductions(g.module); got != 1 {
			g.dispose()
			t.Fatalf("grouped %d reductions, want 1", got)
		}
		if err := g.verify(); err != nil {
			g.dispose()
			t.Fatal(err)
		}
		out := g.printModule()
		multiply := "mul nsw i64 %q"
		if strings.Contains(ir, "mul nuw") {
			multiply = "mul nuw i64 %q"
		}
		if !strings.Contains(out, "add i64 %n, %neg") ||
			!strings.Contains(out, "add i64 %elisa.remainder.grouped, %sum") ||
			!strings.Contains(out, multiply) ||
			(strings.Contains(ir, "udiv exact") && !strings.Contains(out, "udiv exact")) {
			g.dispose()
			t.Fatalf("reduction grouping or multiply flags changed:\n%s", out)
		}
		if strings.Contains(ir, "udiv exact") {
			if !strings.Contains(out, "icmp ult i64 %n,") {
				t.Fatalf("exact quotient changed a defined branch:\n%s", out)
			}
		} else if !strings.Contains(out, "icmp eq i64 %q, 0") {
			t.Fatalf("branch did not reuse its quotient:\n%s", out)
		}
		if groupRemainderReductions(g.module) != 0 {
			g.dispose()
			t.Fatal("grouping is not idempotent")
		}
		g.dispose()
	}
}

func TestGroupRemainderReductionsDeclinesOtherShapes(t *testing.T) {
	cases := map[string]string{
		"zero divisor":          strings.ReplaceAll(remainderReductionIR, "10", "0"),
		"other integer width":   strings.ReplaceAll(remainderReductionIR, "i64", "i32"),
		"signed division":       strings.Replace(remainderReductionIR, "udiv", "sdiv", 1),
		"inner signed nowrap":   strings.Replace(remainderReductionIR, "inner = add", "inner = add nsw", 1),
		"outer unsigned nowrap": strings.Replace(remainderReductionIR, "next = add", "next = add nuw", 1),
		"wrong multiplier":      strings.Replace(remainderReductionIR, ", -10", ", -11", 1),
		"non-phi accumulator":   strings.Replace(remainderReductionIR, "%n, %sum", "%n, %arg", 1),
		"shared inner":          strings.Replace(remainderReductionIR, "  %done =", "  %extra = add i64 %inner, 1\n  %done =", 1),
		"other target":          strings.Replace(remainderReductionIR, "aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu", 1),
		"optnone":               strings.Replace(remainderReductionIR, "@reduce(i64 %arg) {", "@reduce(i64 %arg) noinline optnone {", 1),
	}
	for name, ir := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			if got := groupRemainderReductions(g.module); got != 0 || g.printModule() != before {
				t.Fatalf("unsupported reduction changed %d times", got)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRemainderQuotientBranchConditions(t *testing.T) {
	for _, tc := range []struct {
		name, ir, want string
	}{
		{"unsigned greater or equal", strings.Replace(remainderReductionIR, "icmp ult i64 %n, 10", "icmp uge i64 %n, 10", 1), "icmp ne i64 %q, 0"},
		{"different divisor", strings.Replace(remainderReductionIR, "icmp ult i64 %n, 10", "icmp ult i64 %n, 11", 1), "icmp ult i64 %n, 11"},
		{"signed comparison", strings.Replace(remainderReductionIR, "icmp ult i64 %n, 10", "icmp slt i64 %n, 10", 1), "icmp slt i64 %n, 10"},
		{"same sign", strings.Replace(remainderReductionIR, "icmp ult i64 %n, 10", "icmp samesign ult i64 %n, 10", 1), "icmp eq i64 %q, 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if got := groupRemainderReductions(g.module); got != 1 {
				t.Fatalf("grouped %d reductions, want 1", got)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if out := g.printModule(); !strings.Contains(out, tc.want) {
				t.Fatalf("missing %q in:\n%s", tc.want, out)
			}
		})
	}
}

func TestRemainderQuotientBranchLeavesDarwinComparison(t *testing.T) {
	ir := strings.Replace(remainderReductionIR, "aarch64-unknown-linux-gnu", "arm64-apple-darwin", 1)
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if got := groupRemainderReductions(g.module); got != 1 {
		t.Fatalf("grouped %d reductions, want 1", got)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	if out := g.printModule(); !strings.Contains(out, "icmp ult i64 %n, 10") || strings.Contains(out, "icmp eq i64 %q, 0") {
		t.Fatalf("Darwin comparison changed:\n%s", out)
	}
}
