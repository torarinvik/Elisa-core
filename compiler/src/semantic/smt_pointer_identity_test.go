package semantic

import "testing"

func TestSMTPointerIdentityIsStrongerThanNullness(t *testing.T) {
	for _, tc := range []struct {
		name, contract string
		reject         bool
	}{
		{"reflexive", "requires p != null\n    ensure p == p", false},
		{"symmetric", "requires p == q\n    ensure q == p", false},
		{"nonnull", "requires p != null and q != null\n    ensure p == q", true},
		{"same_nullness", "requires (p == null) == (q == null)\n    ensure p == q", true},
		{"both_null", "requires p == null and q == null\n    ensure p == q", false},
		{"equal_nullness", "requires p == q\n    ensure (p == null) == (q == null)", false},
		{"different_nullness", "requires p == null and q != null\n    ensure p != q", false},
		{"disequality", "requires p != q\n    ensure p == q", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "pointer_identity.elisa", "def check(p: void&?, q: void&?) -> bool:\n    "+tc.contract+"\n    return true\n")
			if rejected := ensureFailuresFor(r.Errors(), "check") != 0; rejected != tc.reject {
				t.Fatalf("rejected %v, want %v:\n%s", rejected, tc.reject, allDiagnostics(r))
			}
		})
	}
}
