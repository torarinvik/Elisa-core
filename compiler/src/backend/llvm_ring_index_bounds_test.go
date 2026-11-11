//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
)

func TestRingIndexBoundsRequiresStableLimitWithinCapacity(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
declare void @use(i64)
declare void @llvm.trap()
define void @ring(i1 noundef %flag, i64 %iterations) {
entry:
 %limit = select i1 %flag, i64 3, i64 6
 br label %loop
loop:
 %index = phi i64 [ 0, %entry ], [ %wrapped, %body ]
 %left = phi i64 [ %iterations, %entry ], [ %remaining, %body ]
 %inbounds = icmp ult i64 %index, 6
 br i1 %inbounds, label %body, label %failure
body:
 call void @use(i64 %index)
 %next = add i64 %index, 1
 %wrap = icmp eq i64 %next, %limit
 %wrapped = select i1 %wrap, i64 0, i64 %next
 %remaining = sub i64 %left, 1
 %done = icmp eq i64 %remaining, 0
 br i1 %done, label %exit, label %loop
failure:
 call void @llvm.trap()
 unreachable
exit:
 ret void
}
`
	for _, test := range []struct {
		name string
		old  string
		new  string
		fold bool
	}{
		{name: "bounded", fold: true},
		{name: "capacity too small", old: "ult i64 %index, 6", new: "ult i64 %index, 5"},
		{name: "possibly zero limit", old: "i64 3, i64 6", new: "i64 0, i64 6"},
		{name: "possibly undefined limit", old: "i1 noundef %flag", new: "i1 %flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(strings.Replace(source, test.old, test.new, 1))
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
