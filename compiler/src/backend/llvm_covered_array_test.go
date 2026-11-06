//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestCoveredArrayInitialization(t *testing.T) {
	if runtime.GOARCH != "arm64" || !scratchPrefixSupported() {
		t.Skip("LLVM 23 AArch64 optimization")
	}
	data, err := os.ReadFile("testdata/covered_array_nested.ll")
	if err != nil {
		t.Fatal(err)
	}
	base := string(data)
	replace := func(s, a, b string) string {
		if strings.Count(s, a) != 1 {
			t.Fatalf("fixture changed: %q", a)
		}
		return strings.Replace(s, a, b, 1)
	}
	shifted := replace(base, "  %elem.ptr = getelementptr [8 x i8], ptr %a, i64 %add76", "  %wrong.index = add i64 %add76, 1\n  %elem.ptr = getelementptr [8 x i8], ptr %a, i64 %wrong.index")
	escaped := replace(base, "  br label %while.head\n\nwhile.head:", "  call void @escape(ptr %a)\n  br label %while.head\n\nwhile.head:") + "\ndeclare void @escape(ptr)\n"
	bound := replace(base, "%cmp155 = icmp ult i64 %s.0, %scalar.ref.elem54", "%cmp155 = icmp ult i64 %s.0, %scalar.ref.elem51")
	store := "  store double %fdiv, ptr %elem.ptr, align 8\n  %add86 = add i64 %p.0, 1"
	skipped := replace(base, store, "  %skip = icmp eq i64 %p.0, 0\n  br i1 %skip, label %after.store, label %do.store\ndo.store:\n  store double %fdiv, ptr %elem.ptr, align 8\n  br label %after.store\nafter.store:\n  %add86 = add i64 %p.0, 1")
	skipped = replace(skipped, "[ %add86, %wd.ok ]", "[ %add86, %after.store ]")
	early := replace(base, store, "  %break = icmp eq i64 %p.0, 0\n  br i1 %break, label %while.exit66, label %do.store\ndo.store:\n  store double %fdiv, ptr %elem.ptr, align 8\n  %add86 = add i64 %p.0, 1")
	early = replace(early, "[ %add86, %wd.ok ]", "[ %add86, %do.store ]")
	clearA := "  call void @llvm.memset.p0.i64(ptr noundef nonnull align 8 dereferenceable(32768) %a, i8 0, i64 32768, i1 false)"
	libc := replace(base, clearA, "  %libc.zero = call ptr @memset(ptr %a, i32 0, i64 32768)") + "\ndeclare ptr @memset(ptr, i32, i64)\n"
	nobuiltin := replace(base, clearA, "  %libc.zero = call ptr @memset(ptr %a, i32 0, i64 32768) #77") + "\ndeclare ptr @memset(ptr, i32, i64)\nattributes #77 = { nobuiltin }\n"
	volatile := replace(base, "%elem179 = load double, ptr %elem.ptr178", "%elem179 = load volatile double, ptr %elem.ptr178")
	for _, tc := range []struct {
		name, ir string
		want     int
	}{
		{"complete coverage", base, 2}, {"shifted write", shifted, 1}, {"escape", escaped, 1},
		{"different bound", bound, 0}, {"skipped write", skipped, 1}, {"early exit", early, 1},
		{"libc memset", libc, 2}, {"nobuiltin memset", nobuiltin, 1}, {"volatile read", volatile, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, e := parseLLVMIRForSplitHalfTest(tc.ir)
			if e != nil {
				t.Fatal(e)
			}
			defer g.dispose()
			if e = g.verify(); e != nil {
				t.Fatalf("invalid fixture: %v", e)
			}
			if n := elideScratchPrefixInitializers(g.module); n != tc.want {
				t.Fatalf("removed %d clears; want %d", n, tc.want)
			}
			if e = g.verify(); e != nil {
				t.Fatal(e)
			}
			if n := elideScratchPrefixInitializers(g.module); n != 0 {
				t.Fatalf("second pass removed %d additional clears", n)
			}
		})
	}
}
