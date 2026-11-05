//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"strings"
	"testing"
)

func TestValidatedIndexBounds(t *testing.T) {
	if !boundedDivisionUnrollSupported() {
		t.Skip("requires LLVM 23")
	}
	data, err := os.ReadFile("testdata/index_bounds.ll")
	if err != nil {
		t.Fatal(err)
	}
	fixture := string(data)
	tests := map[string]struct {
		ir   string
		want int
	}{
		"paired checks":           {fixture, 1},
		"unbounded signed bound":  {strings.Replace(fixture, "%n = and i64 %raw, 63", "%n = add i64 %raw, 0", 1), 0},
		"negative signed bound":   {strings.Replace(fixture, "%n = and i64 %raw, 63", "%n = or i64 %raw, -64", 1), 0},
		"different error results": {strings.Replace(fixture, "[ -1, %upper ]", "[ -2, %upper ]", 1), 0},
		"observable upper check":  {strings.Replace(fixture, "upper:\n", "upper:\n call void @observable()\n", 1), 0},
		"continue phi":            {strings.Replace(fixture, "valid:\n", "valid:\n %seen = phi i64 [ 1, %upper ]\n", 1), 0},
		"sanitized":               {strings.Replace(fixture, "%raw) {", "%raw) sanitize_address {", 1), 0},
		"size":                    {strings.Replace(fixture, "%raw) {", "%raw) optsize {", 1), 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if n := proveIndexBounds(g); n != tc.want {
				t.Fatalf("changed %d, want %d", n, tc.want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			if n := proveIndexBounds(g); n != 0 {
				t.Fatalf("second pass changed %d", n)
			}
		})
	}
}
