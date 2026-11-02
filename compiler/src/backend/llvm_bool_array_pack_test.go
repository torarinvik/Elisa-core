//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const boolArrayHeader = `target datalayout = "e-p:64:64-i64:64-n8:16:32:64-S128"
target triple = "aarch64-unknown-linux-gnu"
`
const boolArrayProbe = `define i1 @probe(i64 %write, i64 %read, i1 %value) {
 %a = alloca [4 x i1], align 4
 store i32 0, ptr %a, align 4
 %p = getelementptr inbounds [4 x i1], ptr %a, i64 0, i64 %write
 store i1 %value, ptr %p, align 1
 %q = getelementptr inbounds i8, ptr %a, i64 %read
 %r = load i1, ptr %q, align 1
 ret i1 %r
}
`

func TestPrivateBoolArrayPackingAndPoisonIsolation(t *testing.T) {
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + boolArrayProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if result := packPrivateBoolArrays(g); result != 1 {
		t.Fatalf("packing status %d", result)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	ir := g.printModule()
	if strings.Contains(ir, "alloca [4 x i1]") || !strings.Contains(ir, "freeze i1 %value") {
		t.Fatalf("missing scalar promotion or poison isolation:\n%s", ir)
	}
	if result := packPrivateBoolArrays(g); result != 0 {
		t.Fatalf("second packing status %d", result)
	}
	if ir != g.printModule() {
		t.Fatal("second application changed module")
	}
}

func TestPrivateBoolArrayPackingDeclinesObservableMemory(t *testing.T) {
	cases := map[string]string{
		"debug module":         boolArrayProbe + "\n!llvm.dbg.cu = !{}\n!llvm.module.flags = !{!0}\n!0 = !{i32 2, !\"Debug Info Version\", i32 3}\n",
		"too many elements":    strings.ReplaceAll(strings.Replace(boolArrayProbe, "store i32 0, ptr %a, align 4", "store i1 false, ptr %a, align 1", 1), "[4 x i1]", "[65 x i1]"),
		"dynamic memset":       strings.Replace(boolArrayProbe, " store i32 0, ptr %a, align 4", " call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 %write, i1 false)", 1) + "declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)\n",
		"escape":               strings.Replace(boolArrayProbe, " store i32 0,", " call void @escape(ptr %a)\n store i32 0,", 1) + "declare void @escape(ptr)\n",
		"volatile":             strings.Replace(boolArrayProbe, "load i1,", "load volatile i1,", 1),
		"atomic":               strings.Replace(boolArrayProbe, "store i32 0, ptr %a, align 4", "store atomic i32 0, ptr %a monotonic, align 4", 1),
		"byte load":            strings.Replace(boolArrayProbe, " %r = load i1,", " %byte = load i8, ptr %a, align 1\n %r = load i1,", 1),
		"nonzero wide store":   strings.Replace(boolArrayProbe, "store i32 0,", "store i32 7,", 1),
		"metadata":             strings.Replace(boolArrayProbe, "%r = load i1, ptr %q, align 1", "%r = load i1, ptr %q, align 1, !test !0", 1) + "!0 = !{}\n",
		"size optimized":       strings.Replace(boolArrayProbe, "i1 %value) {", "i1 %value) optsize {", 1),
		"non inbounds pointer": strings.ReplaceAll(boolArrayProbe, "getelementptr inbounds", "getelementptr"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + body)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			before := g.printModule()
			if result := packPrivateBoolArrays(g); result != 0 {
				t.Fatalf("unsupported packing status %d", result)
			}
			if before != g.printModule() {
				t.Fatal("declined operation changed module")
			}
		})
	}
}

func boolArrayRoundTrip(n int) string {
	return fmt.Sprintf(`define i64 @roundtrip%d(i64 %%initial, i64 %%write, i1 %%value) {
entry:
 %%a = alloca [%d x i1], align 1
 %%valid = icmp ult i64 %%write, %d
 br i1 %%valid, label %%init, label %%invalid
invalid:
 ret i64 %%initial
init:
 %%i = phi i64 [0, %%entry], [%%next, %%init]
 %%shift = lshr i64 %%initial, %%i
 %%bit = trunc i64 %%shift to i1
 %%p = getelementptr inbounds [%d x i1], ptr %%a, i64 0, i64 %%i
 store i1 %%bit, ptr %%p, align 1
 %%next = add i64 %%i, 1
 %%more = icmp ult i64 %%next, %d
 br i1 %%more, label %%init, label %%update
update:
 %%w = getelementptr inbounds i8, ptr %%a, i64 %%write
 store i1 %%value, ptr %%w, align 1
 br label %%read
read:
 %%j = phi i64 [0, %%update], [%%nextj, %%read]
 %%result = phi i64 [0, %%update], [%%combined, %%read]
 %%q = getelementptr inbounds i8, ptr %%a, i64 %%j
 %%got = load i1, ptr %%q, align 1
 %%wide = zext i1 %%got to i64
 %%shifted = shl i64 %%wide, %%j
 %%combined = or i64 %%result, %%shifted
 %%nextj = add i64 %%j, 1
 %%again = icmp ult i64 %%nextj, %d
 br i1 %%again, label %%read, label %%exit
exit:
 ret i64 %%combined
}
`, n, n, n, n, n, n)
}

func TestPrivateBoolArrayPackingNativeRoundTrips(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 optimization")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	ir := boolArrayHeader
	for _, n := range []int{2, 4, 16, 64} {
		ir += boolArrayRoundTrip(n)
	}
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if result := packPrivateBoolArrays(g); result != 1 {
		t.Fatalf("packing status %d", result)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "packed.o")
	bin := filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(clang, "-O2", filepath.Join("testdata", "bool_array_pack.c"), obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, output)
	}
	if output, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("round trips: %v\n%s", err, output)
	}
}
