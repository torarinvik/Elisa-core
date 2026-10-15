//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ROBUSTNESS (q3 stack overflow): translating a self-recursive call proves its termination
// certificate and its callee `requires` with SMT goals whose hypotheses include the caller's
// immutable locals. When a local is DEFINED by that same call (`prior = f(..., end - 1)`), the
// goal re-translated the call, which re-proved the certificate, and so on until the Go stack
// overflowed. The re-entrant request now declines (assumes nothing about the call) and analysis
// finishes. The first case is the shape of stage1's `matching_struct_count_class_prefix`, which crashed the probe.
func TestSMTRecursiveCallDefiningLocalDoesNotRecurseForever(t *testing.T) {
	cases := map[string]string{
		"ghost_count_field": `
struct Sym:
    kind: i64

struct Table:
    symbols: darray[Sym]

def class_prefix(table: Table&, end: usize) -> u8:
    requires end <= table.symbols.count
    decreases end
    return 0 if end == 0
    prior_end: usize = end - 1
    prior_count: u8 = class_prefix(table, prior_end)
    return 2 if prior_count == 2
    return prior_count
`,
		"view_len": `
def count(s: sview, i: usize) -> usize:
    requires i <= s.len
    decreases s.len - i
    ensure result <= s.len
    if i == s.len:
        return i
    r: usize = count(s, i + 1)
    return r
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeContractStrict(t, name+".elisa", src)
			if name == "ghost_count_field" && len(result.Errors()) != 0 {
				t.Fatalf("the saturating prefix count is valid and must analyze cleanly; got: %v", result.Errors())
			}
			for _, e := range result.Errors() {
				if strings.Contains(e, "expected") || strings.Contains(e, "unknown") {
					t.Fatalf("test program is invalid: %v", result.Errors())
				}
			}
		})
	}
}
