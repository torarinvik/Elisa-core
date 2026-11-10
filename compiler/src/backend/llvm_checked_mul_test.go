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

const checkedMulBatchIR = `define i64 @mulbatch(ptr %a, ptr %b, ptr %out, i64 %n) nounwind {
entry:
 br label %head
head:
 %i = phi i64 [0, %entry], [%next, %commit]
 %more = icmp ult i64 %i, %n
 br i1 %more, label %body, label %exit
body:
 %ap = getelementptr i64, ptr %a, i64 %i
 %bp = getelementptr i64, ptr %b, i64 %i
 %av = load i64, ptr %ap, align 8
 %bv = load i64, ptr %bp, align 8
 %pair = call {i64, i1} @llvm.smul.with.overflow.i64(i64 %av, i64 %bv)
 %overflow = extractvalue {i64, i1} %pair, 1
 br i1 %overflow, label %invalid, label %commit
commit:
 %value = extractvalue {i64, i1} %pair, 0
 %p = getelementptr i64, ptr %out, i64 %i
 store i64 %value, ptr %p, align 8
 %next = add i64 %i, 1
 br label %head
invalid:
 ret i64 -777
exit:
 ret i64 %n
}
declare {i64, i1} @llvm.smul.with.overflow.i64(i64, i64)
`

func TestCheckedMulFastPathAndIdempotency(t *testing.T) {
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + checkedMulBatchIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := fastCheckedMul(g); n != 1 {
		t.Fatalf("status %d", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	ir := g.printModule()
	for _, required := range []string{"freeze i64 %av", "freeze i64 %bv", "checkedmul.slow:", "elisa.checkedmul.versioned", "icmp ult i64", "@llvm.smul.with.overflow.i64"} {
		if !strings.Contains(ir, required) {
			t.Fatalf("missing %q:\n%s", required, ir)
		}
	}
	if n := fastCheckedMul(g); n != 0 || ir != g.printModule() {
		t.Fatalf("not idempotent: %d", n)
	}
}

func TestCheckedMulFastPathRejectsUnsupportedCases(t *testing.T) {
	scalar := `define i64 @scalar(i64 %a, i64 %b) nounwind {
 %p = call {i64, i1} @llvm.smul.with.overflow.i64(i64 %a, i64 %b)
 %f = extractvalue {i64, i1} %p, 1
 br i1 %f, label %bad, label %ok
bad:
 ret i64 -777
ok:
 %v = extractvalue {i64, i1} %p, 0
 ret i64 %v
}
declare {i64, i1} @llvm.smul.with.overflow.i64(i64, i64)
`
	outer := strings.Replace(checkedMulBatchIR, "[%next, %commit]", "[%next, %latch]", 1)
	outer = strings.Replace(outer, "commit:\n %value =", "commit:\n br label %inner\ninner:\n %j = phi i64 [0, %commit], [%jnext, %inner]\n %jnext = add i64 %j, 1\n %again = icmp ult i64 %jnext, 4\n br i1 %again, label %inner, label %latch\nlatch:\n %value =", 1)
	cases := map[string]string{
		"not innermost":       outer,
		"not in a loop":       scalar,
		"unsigned":            strings.ReplaceAll(checkedMulBatchIR, "@llvm.smul.", "@llvm.umul."),
		"i32":                 strings.ReplaceAll(checkedMulBatchIR, "i64", "i32"),
		"constant multiplier": strings.Replace(checkedMulBatchIR, "i64 %av, i64 %bv)", "i64 %av, i64 2)", 1),
		"metadata":            strings.Replace(checkedMulBatchIR, "i64 %av, i64 %bv)", "i64 %av, i64 %bv), !test !0", 1) + "!0 = !{}\n",
		"optnone":             strings.Replace(checkedMulBatchIR, "nounwind {", "nounwind noinline optnone {", 1),
		"optsize":             strings.Replace(checkedMulBatchIR, "nounwind {", "nounwind optsize {", 1),
		"no overflow guard":   strings.Replace(checkedMulBatchIR, "br i1 %overflow,", "br i1 false,", 1),
		"debug":               checkedMulBatchIR + "\n!llvm.dbg.cu = !{}\n!llvm.module.flags = !{!0}\n!0 = !{i32 2, !\"Debug Info Version\", i32 3}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + body)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			if n := fastCheckedMul(g); n != 0 || before != g.printModule() {
				t.Fatalf("unexpected modification: %d", n)
			}
		})
	}
}

func TestCheckedSquareOuterLoopRange(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const body = `define i64 @square_loop() {
entry:
 br label %outer
outer:
 %i = phi i64 [0, %entry], [%next, %latch]
 %square = call {i64, i1} @llvm.smul.with.overflow.i64(i64 %i, i64 %i)
 %overflow = extractvalue {i64, i1} %square, 1
 br i1 %overflow, label %bad, label %preinner
preinner:
 %value = extractvalue {i64, i1} %square, 0
 br label %inner
inner:
 %j = phi i64 [0, %preinner], [%jnext, %inner]
 %jnext = add i64 %j, 1
 %innermore = icmp ult i64 %jnext, 4
 br i1 %innermore, label %inner, label %latch
latch:
 %next = add nuw nsw i64 %i, 1
 %done = icmp eq i64 %next, LIMIT
 br i1 %done, label %exit, label %outer
bad:
 ret i64 -1
exit:
 ret i64 %value
}
declare {i64, i1} @llvm.smul.with.overflow.i64(i64, i64)
`
	for _, tt := range []struct {
		name, limit string
		fold        bool
	}{
		{"largest safe factor", "3037000500", true},
		{"first overflowing factor", "3037000501", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + strings.ReplaceAll(body, "LIMIT", tt.limit))
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if n := fastCheckedMul(g); (n == 1) != tt.fold || n > 1 {
				t.Fatalf("unexpected pass status %d", n)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			ir := g.printModule()
			if strings.Contains(ir, "call { i64, i1 } @llvm.smul.with.overflow") == tt.fold {
				t.Fatalf("incorrect overflow check in transformed IR:\n%s", ir)
			}
		})
	}
}

func TestCheckedMulFastPathNativeSignedBoundariesAndPrefixWrites(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 optimization")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + checkedMulBatchIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := fastCheckedMul(g); n != 1 {
		t.Fatalf("status %d", n)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "batch.o")
	src := filepath.Join(dir, "check.c")
	bin := filepath.Join(dir, "check")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	code := `#include <stdint.h>
#include <stddef.h>
extern int64_t mulbatch(const int64_t*,const int64_t*,int64_t*,uint64_t);
static int check(const int64_t *a,const int64_t *b,unsigned n){
 int64_t out[19],expected[19];const int64_t guard=INT64_C(0x5739ba3ef1034729);
 for(unsigned i=0;i<19;i++)out[i]=expected[i]=guard;
 int64_t status=n;
 for(unsigned i=0;i<n;i++){int64_t v;if(__builtin_mul_overflow(a[i],b[i],&v)){status=-777;break;}expected[i+1]=v;}
 if(mulbatch(a,b,out+1,n)!=status)return 1;
 for(unsigned i=0;i<19;i++)if(out[i]!=expected[i])return 2;
 return 0;
}
int main(void){
 int64_t edges[]={INT64_MIN,INT64_MIN+1,-4294967296LL,-2147483648LL,-3,-2,-1,0,1,2,3,2147483647LL,4294967295LL,INT64_MAX-1,INT64_MAX};
 int64_t a[17],b[17];
 for(unsigned i=0;i<sizeof(edges)/sizeof(*edges);i++)for(unsigned j=0;j<sizeof(edges)/sizeof(*edges);j++){
  for(unsigned k=0;k<17;k++){a[k]=edges[i];b[k]=edges[j];}
  for(unsigned n=0;n<=17;n++)if(check(a,b,n))return 1;
 }
 for(unsigned bad=0;bad<17;bad++){
  for(unsigned k=0;k<17;k++){a[k]=1;b[k]=1;}
  a[bad]=INT64_MIN;b[bad]=-1;
  if(check(a,b,17))return 3;
 }
 // Aliasing output with a following input element must retain sequential writes.
 for(unsigned scenario=0;scenario<3;scenario++){
  int64_t expected[17];for(unsigned i=0;i<17;i++){a[i]=expected[i]=1;b[i]=i%3==0?1:2;}
  if(scenario==1)b[8]=INT64_MIN;
  if(scenario==2)b[3]=0;
  int64_t status=16;
  for(unsigned i=0;i<16;i++){int64_t v;if(__builtin_mul_overflow(expected[i],b[i],&v)){status=-777;break;}expected[i+1]=v;}
  if(mulbatch(a,b,a+1,16)!=status)return 4;
  for(unsigned i=0;i<17;i++)if(a[i]!=expected[i])return 5;
 }
 uint64_t state=639171;
 for(unsigned round=0;round<1024;round++){
  for(unsigned i=0;i<17;i++){state^=state<<13;state^=state>>7;state^=state<<17;a[i]=(int64_t)state;
   state^=state<<13;state^=state>>7;state^=state<<17;b[i]=round%3==0?round%2:(round%3==1?(int64_t)(state%31)-15:(int64_t)state);}
  if(check(a,b,17))return 2;
 }
 return 0;
}`
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

func TestCheckedMulFastPathRepairsSuccessAndOverflowPHIs(t *testing.T) {
	body := strings.Replace(checkedMulBatchIR, " %overflow =", " %value = extractvalue {i64, i1} %pair, 0\n %overflow =", 1)
	body = strings.Replace(body, "commit:\n %value = extractvalue {i64, i1} %pair, 0", "commit:\n %success = phi i64 [%value, %body]", 1)
	body = strings.Replace(body, "store i64 %value,", "store i64 %success,", 1)
	body = strings.Replace(body, "invalid:\n ret i64 -777", "invalid:\n %error = phi i64 [-777, %body]\n ret i64 %error", 1)
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + body)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	if n := fastCheckedMul(g); n != 1 {
		t.Fatalf("status %d", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
}
