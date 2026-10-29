//go:build cgo && (darwin || linux)

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validationPeelIR = `
define i64 @scan(ptr %input, i64 %n) {
entry:
 %empty = icmp eq i64 %n, 0
 br i1 %empty, label %done, label %body
body:
 %i = phi i64 [ 0, %entry ], [ %next, %latch ]
 %p = getelementptr i64, ptr %input, i64 %i
 %v = load i64, ptr %p, align 8
 %invalid = icmp ugt i64 %v, 255
 br i1 %invalid, label %bad, label %latch
latch:
 %next = add nuw i64 %i, 1
 %last = icmp eq i64 %next, %n
 br i1 %last, label %done, label %body
done:
 ret i64 0
bad:
 ret i64 -1
}
`

func TestValidationPeelLoopSafety(t *testing.T) {
	cases := []struct {
		name, ir string
		change   bool
	}{
		{"eligible", validationPeelIR, true},
		{"volatile", strings.Replace(validationPeelIR, "load i64", "load volatile i64", 1), false},
		{"atomic", strings.Replace(validationPeelIR, "load i64, ptr %p, align 8", "load atomic i64, ptr %p monotonic, align 8", 1), false},
		{"store", strings.Replace(validationPeelIR, "%invalid =", "store i64 %v, ptr %p\n %invalid =", 1), false},
		{"optnone", strings.Replace(validationPeelIR, "define i64 @scan(ptr %input, i64 %n) {", "define i64 @scan(ptr %input, i64 %n) noinline optnone {", 1), false},
		{"optsize", strings.Replace(validationPeelIR, "define i64 @scan(ptr %input, i64 %n) {", "define i64 @scan(ptr %input, i64 %n) optsize {", 1), false},
		{"minsize", strings.Replace(validationPeelIR, "define i64 @scan(ptr %input, i64 %n) {", "define i64 @scan(ptr %input, i64 %n) minsize {", 1), false},
		{"loop_metadata", strings.Replace(validationPeelIR, "label %body\ndone:", "label %body, !llvm.loop !0\ndone:", 1) + "\n!0 = distinct !{!0, !1}\n!1 = !{!\"llvm.loop.unroll.disable\"}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			n := peelSmallValidationLoops(g.module)
			want := tc.change && tableUnrollSupported()
			if (n != 0) != want {
				t.Fatalf("peeled %d, want change %v", n, want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if !want && g.printModule() != before {
				t.Fatal("declined loop changed")
			}
		})
	}
}

func TestValidationPeelCacheIdentity(t *testing.T) {
	o3 := optimizedModuleCacheKey(&llvmGenerator{}, OptimizationLevel3, "triple", "cpu", "features")
	if !strings.Contains(o3, "validation-peel-unroll-v2") {
		t.Fatal("missing peeling policy in O3 cache key")
	}
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel1, OptimizationLevel2} {
		if strings.Contains(optimizedModuleCacheKey(&llvmGenerator{}, level, "triple", "cpu", "features"), "validation-peel-unroll-v2") {
			t.Fatal("peeling policy applied at lower optimization level")
		}
	}
	if strings.Contains(optimizedModuleCacheKey(&llvmGenerator{emitDebugInfo: true}, OptimizationLevel3, "triple", "cpu", "features"), "validation-peel-unroll-v2") {
		t.Fatal("peeling policy applied to debug builds")
	}
}

func TestValidationPeelRuntime(t *testing.T) {
	if !tableUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	g, err := parseLLVMIRForSplitHalfTest(validationPeelIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := peelSmallValidationLoops(g.module); n != 1 {
		t.Fatalf("peeled %d loops", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(g.printModule(), "load i64"); got < 4 {
		t.Fatalf("expected unrolled validation loads, got %d", got)
	}
	dir := t.TempDir()
	irPath := filepath.Join(dir, "scan.ll")
	cPath := filepath.Join(dir, "check.c")
	binary := filepath.Join(dir, "check")
	if err := os.WriteFile(irPath, []byte(g.printModule()), 0600); err != nil {
		t.Fatal(err)
	}
	const driver = `#include <stdint.h>
#include <sys/mman.h>
#include <unistd.h>
extern int64_t scan(const int64_t*, int64_t);
int main(void) {
 int64_t a[256]; uint64_t state=7;
 if (scan(0,0)!=0) return 1;
 for (int n=0;n<=256;n++) for (int bad=-1;bad<n;bad++) {
  for (int i=0;i<n;i++) { state^=state<<13;state^=state>>7;state^=state<<17;a[i]=state&255; }
  if (bad>=0) a[bad]=(bad&1)?-1:256;
  if (scan(a,n)!=(bad<0?0:-1)) return 2;
 }
 // An early invalid element must not cause a load from the protected page.
 size_t page=(size_t)sysconf(_SC_PAGESIZE);
 void *map=mmap(0,2*page,PROT_READ|PROT_WRITE,MAP_PRIVATE|MAP_ANON,-1,0);
 if (map==MAP_FAILED || mprotect((char*)map+page,page,PROT_NONE)) return 3;
 for (int count=1;count<=32;count++) {
  int64_t *p=(int64_t*)((char*)map+page)-count;
  for (int i=0;i<count;i++) p[i]=1;
  p[count-1]=-1;
  if (scan(p,count+1)!=-1) return 4;
 }
 munmap(map,2*page);
 return 0;
}`
	if err := os.WriteFile(cPath, []byte(driver), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, clang, "-O0", irPath, cPath, "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, output)
	}
	runCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if output, err := exec.CommandContext(runCtx, binary).CombinedOutput(); err != nil {
		t.Fatalf("runtime: %v\n%s", err, output)
	}
}
