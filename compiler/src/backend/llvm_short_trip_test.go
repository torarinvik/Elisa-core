//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const shortTripProbe = `define i64 @short_trip_probe(ptr %input, i64 %n) nounwind {
entry:
 %a = alloca [256 x i64], align 8
 %badlen = icmp ugt i64 %n, 256
 %empty = icmp eq i64 %n, 0
 %bad = or i1 %badlen, %empty
 br i1 %bad, label %invalid, label %init
invalid:
 ret i64 -777
init:
 call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 2048, i1 false)
 br label %loop
loop:
 %i = phi i64 [0, %init], [%next, %loop]
 %sum = phi i64 [0, %init], [%updated, %loop]
 %p = getelementptr i64, ptr %input, i64 %i
 %v = load i64, ptr %p, align 8
 %dst = getelementptr [256 x i64], ptr %a, i64 0, i64 %i
 store i64 %v, ptr %dst, align 8
 %next = add i64 %i, 1
 %j = urem i64 %next, %n
 %q = getelementptr [256 x i64], ptr %a, i64 0, i64 %j
 %got = load i64, ptr %q, align 8
 %updated = add i64 %sum, %got
 %done = icmp eq i64 %next, %n
 br i1 %done, label %exit, label %loop
exit:
 ret i64 %updated
}
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
`

func TestShortTripSpecializationAndIdempotency(t *testing.T) {
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + shortTripProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := specializeShortTrips(g); n != 1 {
		t.Fatalf("specialization status %d", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	ir := g.printModule()
	if !strings.Contains(ir, "short_trip.dispatch") || !strings.Contains(ir, "elisa.short-trip-versioned") || strings.Contains(ir, "define internal") {
		t.Fatalf("missing inline fast path:\n%s", ir)
	}
	if strings.Count(ir, "call void @llvm.memset") != 1 || strings.Count(ir, "alloca [256 x i64]") != 1 {
		t.Fatalf("fast path retained large initialization or stack allocation:\n%s", ir)
	}
	if n := specializeShortTrips(g); n != 0 || ir != g.printModule() {
		t.Fatalf("not idempotent: %d", n)
	}
}

func TestShortTripSpecializationDeclinesUnsupportedShapes(t *testing.T) {
	cases := map[string]string{
		"small initialization":  strings.ReplaceAll(strings.ReplaceAll(shortTripProbe, "256", "8"), "2048", "64"),
		"nonunit stride":        strings.Replace(shortTripProbe, "%next = add i64 %i, 1", "%next = add i64 %i, 2", 1),
		"nonzero initial index": strings.Replace(shortTripProbe, "[0, %init], [%next", "[1, %init], [%next", 1),
		"variable fill":         strings.Replace(shortTripProbe, "i64 2048, i1 false", "i64 %n, i1 false", 1),
		"volatile fill":         strings.Replace(shortTripProbe, "i64 2048, i1 false", "i64 2048, i1 true", 1),
		"debug module":          shortTripProbe + "\n!llvm.dbg.cu = !{}\n!llvm.module.flags = !{!0}\n!0 = !{i32 2, !\"Debug Info Version\", i32 3}\n",
		"size optimized":        strings.Replace(shortTripProbe, "nounwind {", "nounwind optsize {", 1),
		"escaping call":         strings.Replace(shortTripProbe, " %next = add", " call void @escape(ptr %a)\n %next = add", 1) + "declare void @escape(ptr)\n",
		// The one-iteration version still observes the initialization, so the cost
		// check must decline even though the loop structure otherwise qualifies.
		"initialization still needed": strings.Replace(shortTripProbe, "%j = urem i64 %next, %n", "%j = and i64 %v, 255", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + body)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			if n := specializeShortTrips(g); n != 0 {
				t.Fatalf("declined status %d", n)
			}
			if before != g.printModule() {
				t.Fatal("decline changed original IR")
			}
		})
	}
}

func TestShortTripSpecializationNativeResults(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 optimization")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + shortTripProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := specializeShortTrips(g); n != 1 {
		t.Fatalf("status %d", n)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "probe.o")
	src := filepath.Join(dir, "check.c")
	bin := filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	code := `#include <stdint.h>
#include <stddef.h>
extern int64_t short_trip_probe(const int64_t*,uint64_t);
int main(void){int64_t v[256]; uint64_t state=937123;
 for(unsigned round=0;round<64;round++){
 for(unsigned i=0;i<256;i++){state^=state<<13;state^=state>>7;state^=state<<17;v[i]=(int64_t)state;}
 for(unsigned n=0;n<=257;n++) if(short_trip_probe(v,n)!=(n==0||n>256?-777:v[0]))return 1;
 }return 0;}`
	if err := os.WriteFile(src, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang, "-O2", src, obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("runtime: %v\n%s", err, out)
	}
}

func TestShortTripSpecializationI32Count(t *testing.T) {
	body := strings.ReplaceAll(strings.ReplaceAll(shortTripProbe, "i64", "i32"), "2048", "1024")
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + body)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := specializeShortTrips(g); n != 1 {
		t.Fatalf("status %d", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(g.printModule(), "call void @llvm.memset") != 1 {
		t.Fatal("i32 fast path retained initialization")
	}
}
