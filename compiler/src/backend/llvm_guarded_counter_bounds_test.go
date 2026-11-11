//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

func TestGuardedCounterBoundsRequiresInductiveCap(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
declare void @use(i64)
declare void @llvm.trap()
define void @scan(i1 %record, i1 %repeat) {
entry:
  br label %loop
loop:
  %count = phi i64 [ 0, %entry ], [ %next, %latch ]
  br i1 %record, label %cap, label %latch
cap:
  %full = icmp eq i64 %count, 7
  br i1 %full, label %exit, label %guard
guard:
  %store = icmp slt i64 %count, 3
  br i1 %store, label %check, label %increment
check:
  %in_bounds = icmp ult i64 %count, 3
  br i1 %in_bounds, label %use, label %failure
use:
  call void @use(i64 %count)
  br label %increment
increment:
  %updated = add i64 %count, 1
  br label %latch
latch:
  %next = phi i64 [ %count, %loop ], [ %updated, %increment ]
  br i1 %repeat, label %loop, label %exit
failure:
  call void @llvm.trap()
  unreachable
exit:
  ret void
}
`
	for _, test := range []struct {
		name, old, replacement string
		fold                   bool
	}{
		{name: "bounded", fold: true},
		{name: "negative seed", old: "[ 0, %entry ]", replacement: "[ -1, %entry ]"},
		{name: "seed above cap", old: "[ 0, %entry ]", replacement: "[ 8, %entry ]"},
		{name: "wrong stride", old: "add i64 %count, 1", replacement: "add i64 %count, 2"},
		{name: "cap does not guard update", old: "br i1 %full", replacement: "br i1 %repeat"},
		{name: "different bounds", old: "icmp slt i64 %count, 3", replacement: "icmp slt i64 %count, 4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(strings.Replace(source, test.old, test.replacement, 1))
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
			if hasTrap := strings.Contains(ir, "call void @llvm.trap"); hasTrap == test.fold {
				t.Fatalf("unexpected bounds trap after cleanup:\n%s", ir)
			}
		})
	}
}
