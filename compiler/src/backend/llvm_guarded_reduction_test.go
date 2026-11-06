//go:build cgo && (darwin || linux)

package backend

import (
	"runtime"
	"strings"
	"testing"
)

const guardedReductionIR = `target triple = "arm64-apple-darwin"
define double @dot(ptr %a, ptr %b, i64 %n) {
entry:
 %offset = add i64 %n, -17
 %invalid = icmp ult i64 %offset, -16
 br i1 %invalid, label %exit.bad, label %preheader
exit.bad:
 ret double 0.0
preheader:
 br label %loop
loop:
 %i = phi i64 [0, %preheader], [%next, %loop]
 %sum = phi double [0.0, %preheader], [%total, %loop]
 %ap = getelementptr double, ptr %a, i64 %i
 %bp = getelementptr double, ptr %b, i64 %i
 %av = load double, ptr %ap, align 8
 %bv = load double, ptr %bp, align 8
 %product = fmul double %av, %bv
 %total = fadd double %sum, %product
 %next = add nuw i64 %i, 1
 %done = icmp eq i64 %next, %n
 br i1 %done, label %exit, label %loop
exit:
 ret double %total
}
`

func TestGuardedReductionUnroll(t *testing.T) {
	if runtime.GOARCH != "arm64" || !boundedDivisionUnrollSupported() {
		t.Skip("AArch64 optimization")
	}
	cases := map[string]struct {
		ir   string
		want int
	}{
		"guarded":       {guardedReductionIR, 1},
		"missing guard": {strings.Replace(guardedReductionIR, "icmp ult i64 %offset, -16", "icmp eq i64 %offset, -16", 1), 0},
		"larger bound":  {strings.Replace(guardedReductionIR, "add i64 %n, -17", "add i64 %n, -18", 1), 0},
		"opposite edge": {strings.Replace(guardedReductionIR, "label %exit.bad, label %preheader", "label %preheader, label %exit.bad", 1), 0},
		"volatile load": {strings.Replace(guardedReductionIR, "%av = load double", "%av = load volatile double", 1), 0},
		"write":         {strings.Replace(guardedReductionIR, "%product = fmul", "store double %av, ptr %ap, align 8\n %product = fmul", 1), 0},
		"size":          {strings.Replace(guardedReductionIR, "i64 %n) {", "i64 %n) optsize {", 1), 0},
		"sanitized":     {strings.Replace(guardedReductionIR, "i64 %n) {", "i64 %n) sanitize_address {", 1), 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(c.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			got := unrollGuardedReductionsForTest(g.module)
			if got != c.want {
				t.Fatalf("got %d transformations; want %d", got, c.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
