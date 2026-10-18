//go:build cgo

package semantic

import (
	"os"
	"strings"
	"testing"
)

// A recursive ghost helper needs an explicit step contract when loop induction
// treats its body as opaque. The contract itself must be checked on every return.
func TestSMTEnumTagLookupStepContract(t *testing.T) {
	b, e := os.ReadFile("testdata/smt_enum_tag_lookup.elisa")
	if e != nil {
		t.Fatal(e)
	}
	source := string(b)
	step := "            ensure index < names.count and index < counts.count and names[index] != name implies result == enum_tag_count_from(names, counts, name, index + 1)\n"
	if strings.Count(source, step) != 1 {
		t.Fatal("fixture step contract missing or ambiguous")
	}
	for _, name := range []string{"candidate", "missing_step", "wrong_step"} {
		t.Run(name, func(t *testing.T) {
			s := source
			if name == "missing_step" {
				s = strings.Replace(s, step, "", 1)
			}
			if name == "wrong_step" {
				s = strings.Replace(s, step, strings.Replace(step, "index + 1)", "index + 2)", 1), 1)
			}
			r := analyzeContractStrict(t, "enum_tag_loop.elisa", s)
			es := r.Errors()
			if name == "candidate" && len(es) != 0 {
				t.Fatal(es)
			}
			if name == "missing_step" && !strings.Contains(strings.Join(es, "\n"), "invariant could not be proven") {
				t.Fatalf("missing step must lose induction proof: %v", es)
			}
			if name == "wrong_step" && !strings.Contains(strings.Join(es, "\n"), `ensure postcondition of "enum_tag_count_from" could not be proven`) {
				t.Fatalf("false step contract must be rejected at its definition: %v", es)
			}
		})
	}
}
