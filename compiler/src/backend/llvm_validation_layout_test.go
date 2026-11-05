//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"strings"
	"testing"
)

func TestValidationReturnLayout(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	data, err := os.ReadFile("testdata/validation_layout.ll")
	if err != nil {
		t.Fatal(err)
	}
	fixture := string(data)
	tests := map[string]struct {
		ir   string
		want int
	}{
		"bounded validation chain": {fixture, 5},
		"Darwin":                   {strings.Replace(fixture, "aarch64-unknown-linux-gnu", "arm64-apple-darwin", 1), 5},
		"other target":             {strings.Replace(fixture, "aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu", 1), 0},
		"different errors":         {strings.Replace(fixture, "[ -1, %load2 ]", "[ -2, %load2 ]", 1), 0},
		"volatile":                 {strings.Replace(fixture, "load i64", "load volatile i64", 1), 0},
		"existing profile":         {strings.Replace(fixture, "label %load0, label %invalid", "label %load0, label %invalid, !prof !0", 1) + "\n!0 = !{!\"branch_weights\", i32 1, i32 31}\n", 0},
		"size":                     {strings.Replace(fixture, "%length) {", "%length) optsize {", 1), 0},
		"sanitized":                {strings.Replace(fixture, "%length) {", "%length) sanitize_address {", 1), 0},
		"constant success":         {strings.Replace(fixture, "[ %sum, %success ]", "[ 0, %success ]", 1), 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if n := biasValidationReturns(g); n != tc.want {
				t.Fatalf("changed %d want %d", n, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if n := biasValidationReturns(g); n != 0 {
				t.Fatalf("second pass changed %d", n)
			}
		})
	}
}
