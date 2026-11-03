//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const boundedUnswitchIR = `define i64 @versionloop(ptr %mem, i64 %n, i1 %flag, i1 %early, ptr %counter) nounwind {
entry:
 %old = load volatile i64, ptr %counter
 %new = add i64 %old, 1
 store volatile i64 %new, ptr %counter
 br label %head
head:
 %i = phi i64 [0, %entry], [%next, %latch]
 %more = icmp ult i64 %i, %n
 br i1 %more, label %body, label %exit
body:
 %p = getelementptr i64, ptr %mem, i64 %i
 %value = load i64, ptr %p
 %j = add i64 %i, 1
 %q = getelementptr i64, ptr %mem, i64 %j
 br i1 %flag, label %yes, label %no
yes:
 %a = mul i64 %value, 13
 %b = add i64 %a, %i
 store i64 %b, ptr %q
 br label %latch
no:
 %c = mul i64 %value, 7
 %d = sub i64 %c, %i
 store i64 %d, ptr %q
 br label %latch
latch:
 %next = add i64 %i, 1
 %half = lshr i64 %n, 1
 %at = icmp eq i64 %next, %half
 %stop = and i1 %early, %at
 br i1 %stop, label %failed, label %head
failed:
 %partial = phi i64 [%next, %latch]
 ret i64 %partial
exit:
 %done = phi i64 [%i, %head]
 ret i64 %done
}
`

func TestBoundedUnswitchExitPHIsGuardAndIdempotency(t *testing.T) {
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + boundedUnswitchIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := boundedLoopUnswitch(g); n != 1 {
		t.Fatalf("changed %d", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	ir := g.printModule()
	if !strings.Contains(ir, "elisa.unswitch.versioned") || !strings.Contains(ir, "freeze i1 %flag") {
		t.Fatal(ir)
	}
	if n := boundedLoopUnswitch(g); n != 0 || ir != g.printModule() {
		t.Fatalf("not idempotent: %d", n)
	}
}

func TestBoundedUnswitchExclusions(t *testing.T) {
	cases := map[string]string{
		"optsize":           strings.Replace(boundedUnswitchIR, "nounwind {", "nounwind optsize {", 1),
		"optnone":           strings.Replace(boundedUnswitchIR, "nounwind {", "nounwind noinline optnone {", 1),
		"volatile in loop":  strings.Replace(boundedUnswitchIR, "%value = load i64", "%value = load volatile i64", 1),
		"variant condition": strings.Replace(boundedUnswitchIR, "br i1 %flag,", "%odd = icmp eq i64 %i, 1\n br i1 %odd,", 1),
		"external call":     strings.Replace(boundedUnswitchIR, "body:\n", "body:\n call void @external()\n", 1) + "\ndeclare void @external()\n",
		"alloca":            strings.Replace(boundedUnswitchIR, "body:\n", "body:\n %stack = alloca i64\n", 1),
		"convergent":        strings.Replace(boundedUnswitchIR, "nounwind {", "nounwind convergent {", 1),
		"debug":             boundedUnswitchIR + "\n!llvm.dbg.cu = !{}\n!llvm.module.flags = !{!0}\n!0 = !{i32 2, !\"Debug Info Version\", i32 3}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + body)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if n := boundedLoopUnswitch(g); n != 0 {
				t.Fatalf("unexpected version: %d", n)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBoundedUnswitchNativeAliasEarlyExitAndPreheaderEffects(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip(err)
	}
	g, err := parseLLVMIRForSplitHalfTest(boolArrayHeader + boundedUnswitchIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := boundedLoopUnswitch(g); n != 1 {
		t.Fatalf("changed %d", n)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "loop.o")
	src := filepath.Join(dir, "test.c")
	bin := filepath.Join(dir, "test")
	if err := g.writeObjectFile(obj, OptimizationLevel0); err != nil {
		t.Fatal(err)
	}
	code := `#include <stdint.h>
#include <stdbool.h>
#include <string.h>
extern uint64_t versionloop(uint64_t*,uint64_t,bool,bool,uint64_t*);
int main(void){
 uint64_t state=173891;
 for(unsigned run=0;run<128;run++)for(unsigned n=0;n<=17;n++)for(unsigned flag=0;flag<2;flag++)for(unsigned early=0;early<2;early++){
  uint64_t actual[21],expect[21],counter=run;
  for(unsigned j=0;j<21;j++){state^=state<<13;state^=state>>7;state^=state<<17;actual[j]=expect[j]=state;}
  unsigned count=0;
  for(unsigned i=0;i<n;i++){expect[i+2]=flag?expect[i+1]*13+i:expect[i+1]*7-i;count=i+1;if(early&&count==(n>>1))break;}
  if(versionloop(actual+1,n,flag,early,&counter)!=count)return 1;
  if(counter!=run+1)return 2;
  if(memcmp(actual,expect,sizeof(actual)))return 3;
 }
 return 0;
}`
	if err := os.WriteFile(src, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(clang, "-O2", src, obj, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("runtime %v\n%s", err, out)
	}
}
