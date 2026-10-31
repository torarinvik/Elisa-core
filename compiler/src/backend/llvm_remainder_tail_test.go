//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

const remainderTailIR = `; ModuleID = 'digital_root.elisa'
source_filename = "digital_root.elisa"
target datalayout = "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-n32:64-S128-Fn32"
target triple = "aarch64-unknown-linux-gnu"

; Function Attrs: nofree norecurse nosync nounwind memory(none)
define range(i64 -9223372036854775808, 10) i64 @digital_root(i64 %0) local_unnamed_addr #0 {
entry:
  %.fr.i = freeze i64 %0
  %cmptmp.i = icmp slt i64 %.fr.i, 0
  br i1 %cmptmp.i, label %digital_root_impl.exit, label %while.cond.preheader.i

while.cond.preheader.i:                           ; preds = %entry
  %cmptmp418.i = icmp samesign ugt i64 %.fr.i, 9
  br i1 %cmptmp418.i, label %while.body6.i, label %digital_root_impl.exit

while.cond.loopexit.i:                            ; preds = %while.body6.i
  %cmptmp4.i = icmp sgt i64 %addtmp.i, 9
  br i1 %cmptmp4.i, label %while.body6.i.backedge, label %digital_root_impl.exit

while.body6.i:                                    ; preds = %while.cond.preheader.i, %while.body6.i.backedge
  %total.117.i = phi i64 [ %total.117.i.be, %while.body6.i.backedge ], [ 0, %while.cond.preheader.i ]
  %number.116.i = phi i64 [ %number.116.i.be, %while.body6.i.backedge ], [ %.fr.i, %while.cond.preheader.i ]
  %divtmp.i = udiv i64 %number.116.i, 10
  %.neg.i = mul nsw i64 %divtmp.i, -10
  %elisa.remainder.grouped = add i64 %number.116.i, %.neg.i
  %addtmp.i = add i64 %elisa.remainder.grouped, %total.117.i
  %cmptmp9.not.i = icmp samesign ult i64 %number.116.i, 10
  br i1 %cmptmp9.not.i, label %while.cond.loopexit.i, label %while.body6.i.backedge

while.body6.i.backedge:                           ; preds = %while.body6.i, %while.cond.loopexit.i
  %total.117.i.be = phi i64 [ %addtmp.i, %while.body6.i ], [ 0, %while.cond.loopexit.i ]
  %number.116.i.be = phi i64 [ %divtmp.i, %while.body6.i ], [ %addtmp.i, %while.cond.loopexit.i ]
  br label %while.body6.i

digital_root_impl.exit:                           ; preds = %while.cond.loopexit.i, %entry, %while.cond.preheader.i
  %common.ret.op.i = phi i64 [ -1, %entry ], [ %.fr.i, %while.cond.preheader.i ], [ %addtmp.i, %while.cond.loopexit.i ]
  ret i64 %common.ret.op.i
}

attributes #0 = { nofree norecurse nosync nounwind memory(none) }
`

func TestRemainderTailPass(t *testing.T) {
	cases := []struct {
		name, ir string
		want     int
	}{
		{"ordinary", remainderTailIR, 1},
		{"quotient zero test", strings.Replace(remainderTailIR, "icmp samesign ult i64 %number.116.i, 10", "icmp eq i64 %divtmp.i, 0", 1), 1},
		{"quotient test and dead comparison", strings.Replace(remainderTailIR, "  br i1 %cmptmp9.not.i, label", "  %quotient.zero = icmp eq i64 %divtmp.i, 0\n  br i1 %quotient.zero, label", 1), 1},
		{"multiple uses in one instruction", strings.Replace(remainderTailIR, "icmp sgt i64 %addtmp.i, 9", "icmp eq i64 %addtmp.i, %addtmp.i", 1), 1},
		{"undef input", strings.Replace(remainderTailIR, "[ %.fr.i, %while.cond.preheader.i ]", "[ undef, %while.cond.preheader.i ]", 1), 0},
		{"exact division", strings.Replace(remainderTailIR, "udiv i64", "udiv exact i64", 1), 0},
		{"overflow addition", strings.Replace(remainderTailIR, "%addtmp.i = add i64", "%addtmp.i = add nsw i64", 1), 0},
		{"changed carry", strings.Replace(remainderTailIR, "[ %number.116.i.be, %while.body6.i.backedge ]", "[ %total.117.i.be, %while.body6.i.backedge ]", 1), 0},
		{"changed outer", strings.Replace(remainderTailIR, "[ 0, %while.cond.loopexit.i ]", "[ 1, %while.cond.loopexit.i ]", 1), 0},
		{"extra work", strings.Replace(remainderTailIR, "  %divtmp.i = udiv", "  %extra = add i64 7, 8\n  %divtmp.i = udiv", 1), 0},
		{"zero divisor", strings.ReplaceAll(remainderTailIR, "i64 %number.116.i, 10", "i64 %number.116.i, 0"), 0},
		{"other target", strings.Replace(remainderTailIR, "aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu", 1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if got := peelRemainderTail(g.module); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if tc.want == 1 && (!strings.Contains(g.printModule(), "elisa.tail.sum") || strings.Contains(g.printModule(), "icmp samesign ult i64 %divtmp.i")) {
				t.Fatal("tail missing or samesign retained")
			}
			if peelRemainderTail(g.module) != 0 {
				t.Fatal("pass is not idempotent")
			}
		})
	}
}
