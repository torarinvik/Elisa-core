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
	if !strings.Contains(ir, "freeze i64 %n") || !strings.Contains(ir, "short_trip.dispatch") || !strings.Contains(ir, "elisa.short-trip-versioned") || strings.Contains(ir, "define internal") {
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

func checkedShortTripProbe(op string) string {
	body := strings.ReplaceAll(shortTripProbe, "@short_trip_probe", "@checked_"+op)
	body = strings.ReplaceAll(body, "[%next, %loop]", "[%next, %commit]")
	body = strings.ReplaceAll(body, "[%updated, %loop]", "[%updated, %commit]")
	body = strings.Replace(body, " %dst =", " %calc = call {i64, i1} @llvm."+op+".with.overflow.i64(i64 %v, i64 2)\n %overflow = extractvalue {i64, i1} %calc, 1\n br i1 %overflow, label %invalid, label %commit\ncommit:\n %checked = extractvalue {i64, i1} %calc, 0\n %dst =", 1)
	body = strings.Replace(body, "store i64 %v,", "store i64 %checked,", 1)
	body = strings.Replace(body, "declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)", "", 1)
	return body + "declare {i64, i1} @llvm." + op + ".with.overflow.i64(i64, i64)\n"
}

func TestShortTripSpecializationPreservesCheckedArithmetic(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 optimization")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	ir := boolArrayHeader + "declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)\n"
	for _, op := range []string{"sadd", "ssub", "smul", "uadd", "usub", "umul"} {
		ir += checkedShortTripProbe(op)
	}
	g, err := parseLLVMIRForSplitHalfTest(ir)
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
	if strings.Count(g.printModule(), "short_trip.dispatch:") != 6 {
		t.Fatal("checked kernels did not specialize")
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "checked.o")
	src := filepath.Join(dir, "check.c")
	bin := filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	code := `#include <stdint.h>
#include <stddef.h>
#define DECL(op) extern int64_t checked_##op(const int64_t*,uint64_t);
DECL(sadd) DECL(ssub) DECL(smul) DECL(uadd) DECL(usub) DECL(umul)
static int calc(unsigned op, uint64_t a, uint64_t *out) {
 int64_t s=0; int overflow=0;
 switch(op){
 case 0: overflow=__builtin_add_overflow((int64_t)a,INT64_C(2),&s);*out=(uint64_t)s;break;
 case 1: overflow=__builtin_sub_overflow((int64_t)a,INT64_C(2),&s);*out=(uint64_t)s;break;
 case 2: overflow=__builtin_mul_overflow((int64_t)a,INT64_C(2),&s);*out=(uint64_t)s;break;
 case 3: overflow=__builtin_add_overflow(a,UINT64_C(2),out);break;
 case 4: overflow=__builtin_sub_overflow(a,UINT64_C(2),out);break;
 case 5: overflow=__builtin_mul_overflow(a,UINT64_C(2),out);break;
 }return overflow;
}
int main(void){
 int64_t (*functions[])(const int64_t*,uint64_t)={checked_sadd,checked_ssub,checked_smul,checked_uadd,checked_usub,checked_umul};
 uint64_t edges[]={0,1,2,3,INT64_MAX-2,INT64_MAX-1,INT64_MAX,UINT64_C(1)<<63,(UINT64_C(1)<<63)+1,(UINT64_C(1)<<63)+2,UINT64_MAX-2,UINT64_MAX-1,UINT64_MAX,(UINT64_C(1)<<62)-1,UINT64_C(1)<<62,(UINT64_C(1)<<62)+1,(UINT64_C(3)<<62),(UINT64_C(3)<<62)-1};
 int64_t v[256];uint64_t state=93172;
 for(unsigned op=0;op<6;op++)for(unsigned round=0;round<18;round++){
  for(unsigned j=0;j<256;j++){state^=state<<13;state^=state>>7;state^=state<<17;v[j]=(int64_t)state;}
  v[0]=(int64_t)edges[round%18];
  for(unsigned n=0;n<=257;n++){
   uint64_t expected=(uint64_t)-777,first=0;
   if(n>0&&n<=256){int overflow=0;for(unsigned j=0;j<n;j++){uint64_t x;overflow|=calc(op,(uint64_t)v[j],&x);if(j==0)first=x;}if(!overflow)expected=first;}
   if((uint64_t)functions[op](v,n)!=expected)return 1;
  }
 }return 0;
}`
	if err := os.WriteFile(src, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang, "-O2", src, obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("checked arithmetic: %v\n%s", err, out)
	}
}

func TestShortTripSpecializationFreezesEarlyDispatch(t *testing.T) {
	body := strings.Replace(shortTripProbe, " %badlen =", " %isnull = icmp eq ptr %input, null\n br i1 %isnull, label %invalid, label %length\nlength:\n %badlen =", 1)
	body += "define i64 @poison_count_early_return() {\n %r = call i64 @short_trip_probe(ptr null, i64 poison)\n ret i64 %r\n}\n"
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
	ir := g.printModule()
	if !strings.Contains(ir, "%short_trip.count = freeze i64 %n") || !strings.Contains(ir, "icmp eq i64 %short_trip.count, 1") {
		t.Fatalf("dispatch could branch on poison:\n%s", ir)
	}
}
