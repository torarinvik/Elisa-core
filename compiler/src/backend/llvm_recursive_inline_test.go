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

const recursiveInlineIR = `target triple = "arm64-apple-darwin"
define private void @walk(ptr %p, i64 %n, i64 %stop) nounwind {
entry:
 %done = icmp eq i64 %n, 0
 br i1 %done, label %exit, label %check
check:
 %bad = icmp eq i64 %n, %stop
 br i1 %bad, label %fail, label %body
fail:
 call void @llvm.trap()
 unreachable
body:
 %old = load i64, ptr %p
 %sum = add i64 %old, %n
 store i64 %sum, ptr %p
 %next = sub i64 %n, 1
 call void @walk(ptr %p, i64 %next, i64 %stop)
 br label %exit
exit:
 ret void
}
define void @run(ptr %p, i64 %n, i64 %stop) {
 call void @walk(ptr %p, i64 %n, i64 %stop)
 ret void
}
declare void @llvm.trap()
`

func TestSmallRecursiveInlineExclusions(t *testing.T) {
	for name, tc := range map[string]struct {
		ir   string
		want int
	}{
		"eligible":           {recursiveInlineIR, 1},
		"noinline":           {strings.Replace(recursiveInlineIR, "nounwind {", "nounwind noinline {", 1), 0},
		"optsize":            {strings.Replace(recursiveInlineIR, "nounwind {", "nounwind optsize {", 1), 0},
		"optnone":            {strings.Replace(recursiveInlineIR, "nounwind {", "nounwind noinline optnone {", 1), 0},
		"external":           {strings.Replace(recursiveInlineIR, "define private void", "define void", 1), 0},
		"volatile":           {strings.Replace(recursiveInlineIR, "load i64", "load volatile i64", 1), 0},
		"linux":              {strings.Replace(recursiveInlineIR, "arm64-apple-darwin", "aarch64-unknown-linux-gnu", 1), 0},
		"address taken":      {recursiveInlineIR + "\n@address = global ptr @walk\n", 0},
		"multiple recursion": {strings.Replace(recursiveInlineIR, " br label %exit\nexit:", " call void @walk(ptr %p, i64 %next, i64 %stop)\n br label %exit\nexit:", 1), 0},
	} {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			if n := inlineSmallRecursion(g.module); n != tc.want {
				t.Fatalf("expanded %d, want %d", n, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			after := g.printModule()
			if tc.want == 0 && before != after {
				t.Fatal("declined transform changed IR")
			}
			if n := inlineSmallRecursion(g.module); n != 0 || g.printModule() != after {
				t.Fatal("not idempotent")
			}
		})
	}
}

func TestSmallRecursiveInlineRuntime(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Apple AArch64")
	}
	cc, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	g, err := parseLLVMIRForSplitHalfTest(recursiveInlineIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if inlineSmallRecursion(g.module) != 1 {
		t.Fatal("not expanded")
	}
	dir := t.TempDir()
	ir := filepath.Join(dir, "walk.ll")
	c := filepath.Join(dir, "main.c")
	bin := filepath.Join(dir, "walk")
	if err := os.WriteFile(ir, []byte(g.printModule()), 0600); err != nil {
		t.Fatal(err)
	}
	harness := `#include <stdint.h>
#include <stdlib.h>
extern void run(int64_t *,int64_t,int64_t);
int main(int argc,char **argv){int64_t x=0;if(argc>1){run(&x,16,atoi(argv[1]));return 1;}
for(int n=0;n<=128;++n){x=0;run(&x,n,-1);if(x!=(int64_t)n*(n+1)/2)return 2;}return 0;}`
	if err := os.WriteFile(c, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cc, "-O3", ir, c, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	for _, stop := range []string{"1", "2", "3", "8", "15", "16"} {
		err := exec.Command(bin, stop).Run()
		if err == nil || !strings.Contains(err.Error(), "trace/BPT trap") {
			t.Fatalf("stop=%s lost trap: %v", stop, err)
		}
	}
}
