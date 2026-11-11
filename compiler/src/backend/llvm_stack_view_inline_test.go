//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"strings"
	"testing"
)

func TestStackViewInliningRequiresLocalArraysAndLiteralFlag(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	var source strings.Builder
	source.WriteString(`
target triple = "arm64-apple-macosx"
%view = type { ptr, i64 }
define internal void @view_helper(%view %a, %view %b, i1 %flag) norecurse nounwind {
entry:
  %data_a = extractvalue %view %a, 0
  %data_b = extractvalue %view %b, 0
  %delta = select i1 %flag, i32 1, i32 2
`)
	for i := range 8 {
		fmt.Fprintf(&source, "  %%a%d = getelementptr i32, ptr %%data_a, i64 %d\n", i, i)
		fmt.Fprintf(&source, "  %%b%d = getelementptr i32, ptr %%data_b, i64 %d\n", i, i)
		fmt.Fprintf(&source, "  %%x%d = load i32, ptr %%a%d\n", i, i)
		fmt.Fprintf(&source, "  %%y%d = add i32 %%x%d, %%delta\n", i, i)
		fmt.Fprintf(&source, "  store i32 %%y%d, ptr %%b%d\n", i, i)
	}
	source.WriteString(`
  ret void
}
define i32 @caller(ptr %other, i1 %dynamic) {
entry:
  %a = alloca [8 x i32]
  %b = alloca [8 x i32]
  store i32 7, ptr %a
  %view_a = insertvalue %view { ptr poison, i64 8 }, ptr %a, 0
  %view_b = insertvalue %view { ptr poison, i64 8 }, ptr %b, 0
  %view_other = insertvalue %view { ptr poison, i64 8 }, ptr %other, 0
  call void @view_helper(%view %view_a, %view %view_b, i1 true)
  call void @view_helper(%view %view_a, %view %view_b, i1 %dynamic)
  call void @view_helper(%view %view_a, %view %view_other, i1 true)
  %result = load i32, ptr %b
  ret i32 %result
}
`)
	g, err := parseLLVMIRForSplitHalfTest(source.String())
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	if status := unrollBoundedDivisionLoops(g); status != 1 {
		t.Fatalf("expected stack-view inlining, got status %d", status)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	if ir := g.printModule(); strings.Count(ir, "call void @view_helper(") != 2 {
		t.Fatalf("expected the dynamic and nonlocal calls to remain:\n%s", ir)
	}
}
