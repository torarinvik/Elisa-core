//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A recursion path whose guards include one the translator does not model (an sview comparison on a
// darray element) still contributes its modelled guards, so `index == count` exits keep the measure
// decreasing. Dropping a guard only weakens the hypotheses.
func TestSMTPathKeepsTranslatableConjuncts(t *testing.T) {
	good := `
ghost def present(names: darray[sview]&, wanted: sview, index: usize) -> bool:
    requires index <= names.count
    decreases names.count - index
    return false if index == names.count
    return true if names[index] == wanted
    return present(names, wanted, index + 1)
`
	result := analyzeContractStrict(t, "path_conj.elisa", good)
	for _, e := range result.Errors() {
		if strings.Contains(e, "decreases") {
			t.Fatalf("present: expected termination proof, got %v", result.Errors())
		}
	}
	// SOUNDNESS: the modelled guard here does not bound index, so the measure stays unproven.
	bad := `
ghost def present(names: darray[sview]&, wanted: sview, index: usize) -> bool:
    requires index <= names.count
    decreases names.count - index
    return true if names[0] == wanted
    return false if index == names.count + 5
    return present(names, wanted, index + 1)
`
	result = analyzeContractStrict(t, "path_conj_bad.elisa", bad)
	found := false
	for _, e := range result.Errors() {
		if strings.Contains(e, "decreases") {
			found = true
		}
	}
	if !found {
		t.Fatalf("bad: termination must stay unproven; got %v", result.Errors())
	}
}
