//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestFoldBitSpreadRequiresCoveredRange(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("qualified AArch64 targets")
	}
	for _, test := range []struct {
		name string
		mask uint64
		want bool
	}{
		{"fits the spread", 0xffffffff, true},
		{"one bit outside the spread", 0x1ffffffff, false},
		{"full width", ^uint64(0), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ir strings.Builder
			fmt.Fprintf(&ir, "target triple = \"aarch64-apple-macosx15.0.0\"\ndefine i64 @probe(i64 %%x) {\n  %%seed = and i64 %%x, %d\n", test.mask)
			value := "%seed"
			for _, shift := range []int{1, 2, 4, 8, 16} {
				fmt.Fprintf(&ir, "  %%shift%d = lshr i64 %s, %d\n  %%or%d = or i64 %s, %%shift%d\n", shift, value, shift, shift, value, shift)
				value = fmt.Sprintf("%%or%d", shift)
			}
			fmt.Fprintf(&ir, "  %%rounded = add i64 %s, 1\n  ret i64 %%rounded\n}\n", value)
			g, err := parseLLVMIRForSplitHalfTest(ir.String())
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if unrollBoundedDivisionLoops(g) == 2 {
				t.Fatal("LLVM cleanup failed")
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if out := g.printModule(); strings.Contains(out, "@llvm.ctlz") != test.want {
				t.Fatalf("bit spread fold = %v, want %v\n%s", !test.want, test.want, out)
			}
		})
	}
}
