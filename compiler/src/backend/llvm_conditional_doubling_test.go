//go:build cgo && (darwin || linux)

package backend

import (
	"strings"
	"testing"
	"unsafe"

	"elisacore/src/backend/llvmnative/scalarloops"
)

func TestConditionalDoublingPreservesWrappingRecurrence(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @step(i64 %initial, i64 %distance, i1 %repeat) {
entry:
  br label %loop
loop:
  %value = phi i64 [ %initial, %entry ], [ %next, %loop ]
  %within = icmp ult i64 %value, %distance
  %negative = sub i64 0, %distance
  %offset = select i1 %within, i64 %value, i64 %negative
  %next = add i64 %offset, %value
  br i1 %repeat, label %loop, label %exit
exit:
  ret i64 %next
}
`
	for _, test := range []struct {
		name, source string
		wantRewrite  bool
	}{
		{"wrapping", source, true},
		{"overflow flags", strings.Replace(source, "add i64", "add nuw i64", 1), false},
		{"unrelated comparison", strings.Replace(source, "icmp ult i64 %value, %distance", "icmp ult i64 %value, %initial", 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(test.source)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			scalarloops.FoldConditionalDoubling(unsafe.Pointer(g.module))
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			ir := g.printModule()
			if got := strings.Contains(ir, "shl i64 %value, 1") &&
				strings.Contains(ir, "sub i64 %value, %distance"); got != test.wantRewrite {
				t.Fatalf("rewrite=%t, want %t:\n%s", got, test.wantRewrite, ir)
			}
		})
	}
}
