//go:build cgo && (darwin || linux)

package backend

import (
	"elisacore/src/backend/llvmnative/stackviews"
	"fmt"
	"strings"
	"testing"
	"unsafe"
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

func TestSeparateStorageRequiresDisjointViewsAtEveryCall(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
%view = type { ptr, i64 }

define internal void @copy_view(%view %a, %view %b, i1 %flag) nounwind {
entry:
  %from = extractvalue %view %a, 0
  %to = extractvalue %view %b, 0
  %value = load i32, ptr %from
  %delta = zext i1 %flag to i32
  %result = add i32 %value, %delta
  store i32 %result, ptr %to
  ret void
}

define void @caller(i32 %value) {
entry:
  %a = alloca [8 x i32]
  %b = alloca [8 x i32]
  store i32 %value, ptr %a
  %va = insertvalue %view { ptr poison, i64 8 }, ptr %a, 0
  %vb = insertvalue %view { ptr poison, i64 8 }, ptr %b, 0
  call void @copy_view(%view %va, %view %vb, i1 true)
  call void @copy_view(%view %va, %view %vb, i1 false)
  ret void
}
`
	for _, test := range []struct {
		name string
		ir   string
		want bool
	}{
		{"distinct arrays", source, true},
		{"one aliased call", strings.Replace(source, "%view %vb, i1 false", "%view %va, i1 false", 1), false},
		{"address taken", source + "@address = global ptr @copy_view\n", false},
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
			if status := stackviews.ExposeDisjointStorage(unsafe.Pointer(g.module), unsafe.Pointer(g.targetMachine)); status == 2 {
				t.Fatal("LLVM cleanup failed")
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			ir := g.printModule()
			if strings.Contains(ir, `"separate_storage"`) != test.want {
				t.Fatalf("unexpected view separation:\n%s", ir)
			}
		})
	}
}
