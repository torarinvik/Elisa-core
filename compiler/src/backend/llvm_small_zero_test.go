//go:build cgo && (darwin || linux)

package backend

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const smallZeroIR = `target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "aarch64-unknown-linux-gnu"
define void @test_smallzero(ptr %p, i64 %n) {
 call void @llvm.memset.p0.i64(ptr %p, i8 0, i64 %n, i1 false)
 ret void
}
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
`

func TestSmallZeroFillsPreserveFallbackAndAreIdempotent(t *testing.T) {
	g, err := parseLLVMIRForSplitHalfTest(smallZeroIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := expandSmallZeroFills(g.module); n != 1 {
		t.Fatalf("expanded %d fills", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	ir := g.printModule()
	if !strings.Contains(ir, "elisa.smallzero.fallback") || !strings.Contains(ir, "store i128 0") {
		t.Fatal("missing bounded stores or fallback")
	}
	if n := expandSmallZeroFills(g.module); n != 0 {
		t.Fatalf("reexpanded fallback %d times", n)
	}
	if g.printModule() != ir {
		t.Fatal("second application changed module")
	}
}
func TestSmallZeroFillsDeclineUnsupportedOperations(t *testing.T) {
	cases := map[string]string{
		"volatile":       strings.Replace(smallZeroIR, "i1 false)", "i1 true)", 1),
		"nonzero":        strings.Replace(smallZeroIR, "i8 0,", "i8 7,", 1),
		"constant":       strings.Replace(smallZeroIR, "i64 %n, i1", "i64 32, i1", 1),
		"other target":   strings.Replace(smallZeroIR, "aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu", 1),
		"size optimized": strings.Replace(smallZeroIR, "i64 %n) {", "i64 %n) optsize {", 1),
		"name collision": smallZeroIR + "\ndefine void @__elisacore_smallzero_fill() { ret void }\n",
	}
	for name, ir := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			if n := expandSmallZeroFills(g.module); n != 0 {
				t.Fatalf("expanded unsupported fill %d times", n)
			}
			if g.printModule() != before {
				t.Fatal("declined transform changed module")
			}
		})
	}
}

func TestSmallZeroFillsNativeBoundaries(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 transform")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	g, err := parseLLVMIRForSplitHalfTest(smallZeroIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := expandSmallZeroFills(g.module); n != 1 {
		t.Fatalf("expanded %d fills", n)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "fill.o")
	bin := filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang, "-O2", filepath.Join("testdata", "small_zero_bounds.c"), obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("boundaries: %v\n%s", err, out)
	}
}

func TestSmallZeroFillsWithDiscardedValueNames(t *testing.T) {
	g, err := parseLLVMIRForSplitHalfTest(smallZeroIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	discardLLVMValueNames(g.context)
	if n := expandSmallZeroFills(g.module); n != 1 {
		t.Fatalf("expanded %d fills with discarded names", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
}

func TestMediumZeroFillsAppleQualification(t *testing.T) {
	const ir = `target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "arm64-apple-darwin"
define void @test(ptr %p) {
 call void @llvm.memset.p0.i64(ptr align 8 %p, i8 0, i64 2048, i1 false)
 ret void
}
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
`
	cases := map[string]struct {
		input string
		want  int
	}{
		"eligible":       {ir, 1},
		"multiple fills": {strings.Replace(ir, " ret void", " call void @llvm.memset.p0.i64(ptr align 8 %p, i8 0, i64 1024, i1 false)\n ret void", 1), 0},
		"linux":          {strings.Replace(ir, "arm64-apple-darwin", "aarch64-unknown-linux-gnu", 1), 0},
		"volatile":       {strings.Replace(ir, "i1 false)", "i1 true)", 1), 0},
		"nonzero":        {strings.Replace(ir, "i8 0,", "i8 1,", 1), 0},
		"unaligned":      {strings.Replace(ir, "align 8", "align 1", 1), 0},
		"too small":      {strings.Replace(ir, "i64 2048", "i64 896", 1), 0},
		"too large":      {strings.Replace(ir, "i64 2048", "i64 2176", 1), 0},
		"size optimized": {strings.Replace(ir, "ptr %p) {", "ptr %p) optsize {", 1), 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if got := expandSmallZeroFills(g.module); got != tc.want {
				t.Fatalf("expanded %d fills, want %d", got, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			result := g.printModule()
			if strings.Contains(result, "call void @llvm.memset.inline") != (tc.want == 1) {
				t.Fatal("unexpected inline memset")
			}
			if got := expandSmallZeroFills(g.module); got != 0 {
				t.Fatalf("second pass expanded %d fills", got)
			}
		})
	}
}

func TestMediumZeroFillsNativeBoundaries(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Apple AArch64 only")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	const ir = `target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "arm64-apple-darwin"
define void @test_1024(ptr %p) { call void @llvm.memset.p0.i64(ptr align 8 %p, i8 0, i64 1024, i1 false) ret void }
define void @test_2048(ptr %p) { call void @llvm.memset.p0.i64(ptr align 8 %p, i8 0, i64 2048, i1 false) ret void }
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
`
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := expandSmallZeroFills(g.module); n != 2 {
		t.Fatalf("expanded %d fills", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "fill.o")
	bin := filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang, "-O2", filepath.Join("testdata", "medium_zero_bounds.c"), obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("boundaries: %v\n%s", err, out)
	}
}
