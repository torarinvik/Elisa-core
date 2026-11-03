//go:build cgo && (darwin || linux)

package backend

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const nestedScanIR = `define i64 @scan(ptr %a, ptr %b, i64 %n, i64 %outer, i64 %stop) nounwind {
entry:
 %empty = icmp eq i64 %n, 0
 br i1 %empty, label %exit, label %outer.head
outer.head:
 %j = phi i64 [0, %entry], [%jnext, %outer.latch]
 br label %inner.head
inner.head:
 %i = phi i64 [0, %outer.head], [%next, %latch]
 %atstop = icmp eq i64 %i, %stop
 br i1 %atstop, label %exit, label %read
read:
 %p = getelementptr i64, ptr %a, i64 %i
 %x = load i64, ptr %p
 %positive = icmp sgt i64 %x, 0
 br i1 %positive, label %read2, label %latch
read2:
 %q = getelementptr i64, ptr %b, i64 %i
 %y = load i64, ptr %q
 %negative = icmp slt i64 %y, 0
 br i1 %negative, label %write, label %latch
write:
 store i64 %x, ptr %q
 br label %latch
latch:
 %next = add nuw i64 %i, 1
 %done = icmp eq i64 %next, %n
 br i1 %done, label %outer.latch, label %inner.head
outer.latch:
 %jnext = add i64 %j, 1
 %finished = icmp eq i64 %jnext, %outer
 br i1 %finished, label %exit, label %outer.head
exit:
 %result = phi i64 [0, %entry], [%i, %inner.head], [%jnext, %outer.latch]
 ret i64 %result
}
`

func TestNestedScanUnrollAndExclusions(t *testing.T) {
	cases := map[string]struct {
		body string
		want int
	}{
		"nested early exit": {nestedScanIR, 1},
		"volatile":          {strings.Replace(nestedScanIR, "load i64, ptr %p", "load volatile i64, ptr %p", 1), 0},
		"size optimized":    {strings.Replace(nestedScanIR, "nounwind {", "nounwind optsize {", 1), 0},
		"convergent":        {strings.Replace(nestedScanIR, "nounwind {", "nounwind convergent {", 1), 0},
		"macOS":             {strings.Replace(boolArrayHeader, "aarch64-unknown-linux-gnu", "arm64-apple-darwin", 1) + nestedScanIR, 0},
		"other target":      {strings.Replace(boolArrayHeader, "aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu", 1) + nestedScanIR, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ir := tc.body
			if !strings.HasPrefix(ir, "target") {
				ir = boolArrayHeader + ir
			}
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if got := unrollNestedScans(g.module); got != tc.want {
				t.Fatalf("unrolled %d loops, want %d", got, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if tc.want == 1 {
				before := g.printModule()
				if got := unrollNestedScans(g.module); got != 0 || before != g.printModule() {
					t.Fatalf("second application changed module: %d", got)
				}
			}
		})
	}
}

func TestNestedScanUnrollRuntime(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 transform")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + nestedScanIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if got := unrollNestedScans(g.module); got != 1 {
		t.Fatalf("unrolled %d loops", got)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "scan.o")
	bin := filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang, "-O2", filepath.Join("testdata", "nested_scans_runtime.c"), obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("runtime: %v\n%s", err, out)
	}
}
