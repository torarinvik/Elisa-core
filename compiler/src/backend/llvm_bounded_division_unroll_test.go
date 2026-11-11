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

const boundedDivisionLoopIR = `
define i64 @count(i64 %input, i64 %offset) {
entry:
 %valid = icmp ult i64 %input, 100000000
 br i1 %valid, label %loop, label %invalid
loop:
 %digits = phi i64 [ %input, %entry ], [ %next, %loop ]
 %count = phi i64 [ %offset, %entry ], [ %inc, %loop ]
 %inc = add i64 %count, 1
 %next = udiv i64 %digits, 10
 %done = icmp ult i64 %digits, 10
 br i1 %done, label %exit, label %loop
exit:
 ret i64 %inc
invalid:
 ret i64 -1
}
`

func TestBoundedDivisionUnroll(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	g, err := parseLLVMIRForSplitHalfTest(boundedDivisionLoopIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	if got := unrollBoundedDivisionLoops(g); got != 1 {
		t.Fatalf("status %d, want changed", got)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	ir := g.printModule()
	if !strings.Contains(ir, "10000000") {
		t.Fatalf("expected division folded into thresholds: %s", ir)
	}
}

func TestBoundedDivisionUnrollExclusions(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	cases := map[string]string{
		"unknown range": strings.Replace(boundedDivisionLoopIR, "%valid = icmp ult i64 %input, 100000000", "%valid = icmp ne i64 %input, 0", 1),
		"signed":        strings.Replace(boundedDivisionLoopIR, "udiv", "sdiv", 1),
		"sanitize":      strings.Replace(boundedDivisionLoopIR, "i64 %offset) {", "i64 %offset) sanitize_address {", 1),
		"size":          strings.Replace(boundedDivisionLoopIR, "i64 %offset) {", "i64 %offset) optsize {", 1),
		"explicit disable": strings.Replace(boundedDivisionLoopIR, "label %exit, label %loop", "label %exit, label %loop, !llvm.loop !0", 1) + `
!0 = distinct !{!0, !1}
!1 = !{!"llvm.loop.unroll.disable"}
`,
		"memory effects": strings.Replace(boundedDivisionLoopIR, "%inc = add", "store volatile i64 %count, ptr null\n %inc = add", 1),
	}
	for name, ir := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if err := g.ensureTargetMachine(); err != nil {
				t.Fatal(err)
			}
			if got := unrollBoundedDivisionLoops(g); got != 0 {
				t.Fatalf("status %d, want unchanged", got)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBoundedDivisionUnrollRuntime(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	var clang string
	for _, p := range []string{"/opt/homebrew/opt/llvm/bin/clang", "/usr/lib/llvm-23/bin/clang"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			clang = p
			break
		}
	}
	if clang == "" {
		t.Skip("requires matching LLVM clang")
	}
	g, err := parseLLVMIRForSplitHalfTest(boundedDivisionLoopIR)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		t.Fatal(err)
	}
	if got := unrollBoundedDivisionLoops(g); got != 1 {
		t.Fatalf("unroll status %d", got)
	}
	dir := t.TempDir()
	ir := strings.Replace(g.printModule(), "@count(", "@count_new(", 1) + strings.Replace(boundedDivisionLoopIR, "@count(", "@count_base(", 1)
	if err := os.WriteFile(filepath.Join(dir, "loops.ll"), []byte(ir), 0600); err != nil {
		t.Fatal(err)
	}
	harness := `#include <stdint.h>
extern uint64_t count_new(uint64_t,uint64_t), count_base(uint64_t,uint64_t);
static int check(uint64_t n,uint64_t offset) {
 uint64_t expected=UINT64_MAX;
 if(n<100000000){expected=offset+1; for(uint64_t x=n;x>=10;x/=10)++expected;}
 return count_new(n,offset)!=expected || count_base(n,offset)!=expected;
}
int main(void) {
 uint64_t inputs[]={0,1,9,10,99,100,999,1000,9999999,10000000,99999999,100000000,UINT64_MAX};
 uint64_t offsets[]={0,1,UINT64_MAX,INT64_MAX,((uint64_t)INT64_MAX)+1};
 for(unsigned i=0;i<sizeof(inputs)/sizeof(inputs[0]);++i)
  for(unsigned j=0;j<sizeof(offsets)/sizeof(offsets[0]);++j)if(check(inputs[i],offsets[j]))return 1;
 uint64_t state=43176;
 for(unsigned i=0;i<20000;++i){state=state*6364136223846793005ULL+1;if(check(state%100000001,state))return 2;}
 return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "main.c"), []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 29*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
	run(clang, "-O0", filepath.Join(dir, "loops.ll"), filepath.Join(dir, "main.c"), "-o", filepath.Join(dir, "check"))
	run(filepath.Join(dir, "check"))
}

func TestJoinedSignedQuotientRequiresNonnegativeShiftInput(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	const source = `
target triple = "arm64-apple-macosx"
define i64 @quotient(i64 %input, i64 %other, i1 %again) {
entry:
  %stable = freeze i64 %input
  %guard = icmp samesign ult i64 %stable, 4
  br i1 %guard, label %invalid, label %initial
initial:
  %first = lshr i64 %stable, 1
  br label %join
join:
  %n = phi i64 [ %stable, %initial ], [ %other, %backedge ]
  %d = phi i64 [ 2, %initial ], [ 3, %backedge ]
  %q = phi i64 [ %first, %initial ], [ %next, %backedge ]
  br i1 %again, label %backedge, label %result
backedge:
  %next = sdiv i64 %other, 3
  br label %join
result:
  %repeat = sdiv i64 %n, %d
  %sum = add i64 %repeat, %q
  ret i64 %sum
invalid:
  ret i64 0
}
`
	for _, test := range []struct {
		name string
		ir   string
		want int
	}{
		{"nonnegative", source, 1},
		{"signed-input-unknown", strings.Replace(source, "icmp samesign ult", "icmp ult", 1), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(test.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if err := g.ensureTargetMachine(); err != nil {
				t.Fatal(err)
			}
			if got := unrollBoundedDivisionLoops(g); got != test.want {
				t.Fatalf("cleanup status %d, want %d", got, test.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
