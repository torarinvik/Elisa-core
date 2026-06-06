package semantic

import (
	"strings"
	"testing"
)

// docs/76 Phase 2 foundation: the resolved index width and free null sentinel.
func TestEnumIndexWidthAndSentinel(t *testing.T) {
	cases := []struct {
		width    string
		bits     int
		sentinel uint64
		maxNodes uint64
	}{
		{"", 32, 0xFFFFFFFF, 0xFFFFFFFF},
		{"u8", 8, 0xFF, 0xFF},
		{"u16", 16, 0xFFFF, 0xFFFF},
		{"u32", 32, 0xFFFFFFFF, 0xFFFFFFFF},
		{"u64", 64, 0xFFFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF},
	}
	for _, c := range cases {
		et := &EnumType{Name: "T", IndexWidth: c.width}
		if got := et.ResolvedIndexWidthBits(); got != c.bits {
			t.Fatalf("width %q: bits = %d, want %d", c.width, got, c.bits)
		}
		if got := et.NullSentinelValue(); got != c.sentinel {
			t.Fatalf("width %q: sentinel = %#x, want %#x", c.width, got, c.sentinel)
		}
		if got := et.MaxNodeCount(); got != c.maxNodes {
			t.Fatalf("width %q: maxNodes = %#x, want %#x", c.width, got, c.maxNodes)
		}
	}
}

// `(sparse)` requires layout soa.
func TestEnumSparseRequiresSoa(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "spaos.elisa", `packed enum Expr layout aos(sparse):
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "`(sparse)` requires `layout soa`") {
		t.Fatalf("sparse on aos must error, got:\n%s", all)
	}
}

// `(index: uN)` is carried and accepted on soa/aos.
func TestEnumIndexWidthAcceptedOnAos(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "idxaos.elisa", `packed enum Expr layout aos(index: u16):
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("layout aos(index: u16) must be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
	et := enumTypeByName(t, result, "Expr")
	if et.ResolvedIndexWidthBits() != 16 {
		t.Fatalf("expected 16-bit handle, got %d", et.ResolvedIndexWidthBits())
	}
}
