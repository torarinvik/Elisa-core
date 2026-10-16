package semantic

import (
	"strings"
	"testing"
)

// A ghost over a type the prover cannot model (a darray parameter, a tuple result) gets no
// defining equation, but a pure, total return tree is still a valid ghost.
func TestGhostUnmodeledSignaturePureTotalAccepted(t *testing.T) {
	source := `ghost def first_is(names: darray[sview]&, name: sview) -> bool:
    return names.count > 0 and names[0] == name

ghost def first_index_spec(names: darray[sview]&, name: sview) -> i64:
    return 0 if first_is(names, name)
    return -1

ghost def summary_spec(names: darray[sview]&, name: sview) -> (found: bool, count: usize):
    return (first_is(names, name), names.count)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ghost_unmodeled_ok.elisa", source, ensureDiagOpts)
	if diags := allDiagnostics(result); strings.Contains(diags, "ghost function") {
		t.Fatalf("pure total ghost over unmodeled types rejected: %s", diags)
	}
}

// Purity stays mandatory: the same unmodeled signature calling a looping, allocating runtime
// function is rejected.
func TestGhostUnmodeledSignatureImpureCallRejected(t *testing.T) {
	source := `def count_x(names: darray[sview]&) -> i64:
    can Memory.Allocate:
        copy: mutable darray[sview] = []
        for name in names |copy|:
            copy.push(name) if name == "x"
        return copy.count.i64()

ghost def x_count_spec(names: darray[sview]&) -> i64:
    return count_x(names)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ghost_unmodeled_impure.elisa", source, ensureDiagOpts)
	if diags := allDiagnostics(result); !strings.Contains(diags, `ghost function "x_count_spec" must be pure and total`) {
		t.Fatalf("impure ghost over unmodeled types accepted: %s", diags)
	}
}

// Totality stays mandatory: a ghost body that is not a total return tree (a loop) is rejected
// whatever its signature.
func TestGhostUnmodeledSignatureNonTotalBodyRejected(t *testing.T) {
	source := `ghost def scan_spec(names: darray[sview]&) -> (found: bool, at: i64):
    index: mutable usize = 0
    while index < names.count |index|:
        index <- index + 1
    return (false, 0)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ghost_unmodeled_nontotal.elisa", source, ensureDiagOpts)
	if diags := allDiagnostics(result); !strings.Contains(diags, `ghost function "scan_spec" body must be a pure total return tree`) {
		t.Fatalf("non-total ghost over unmodeled types accepted: %s", diags)
	}
}
