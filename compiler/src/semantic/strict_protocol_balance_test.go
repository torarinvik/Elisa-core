package semantic

import (
	"strings"
	"testing"
)

const strictBalanceFilePreamble = `struct File[state Closed | Open]:
	is_open: mutable bool

	derive state:
		Closed when self.is_open == false
		Open when self.is_open == true

def open_file(f: mutable File[Closed]&) ensures f => Open:
	f.is_open <- true

def close_file(f: mutable File[Open]&) ensures f => Closed:
	f.is_open <- false

`

// A method that REQUIRES a state but does not change it (`read_file` needs Open) gets an implicit
// `preserve` — no `ensures` boilerplate — so a protocol chain composes and callers never widen.
func TestStrictBalanceImplicitPreserveComposes(t *testing.T) {
	src := strictBalanceFilePreamble + `def read_file(f: mutable File[Open]&) -> i64:
	return 42

def proper(f: mutable File[Closed]&) ensures f => Closed:
	open_file(f)
	x: i64 = read_file(f)
	close_file(f)
`
	result := analyzeFunctionAnalysisTestSource(t, "strict_balance_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a protocol chain with an unannotated state-preserving method must compose cleanly, got: %v", errs)
	}
}

// A method that CHANGES a borrowed resource's state without declaring it is a strict-balance error:
// the resource must be handed back in the state it was lent in, or declare the transition.
func TestStrictBalanceUndeclaredChangeIsError(t *testing.T) {
	src := strictBalanceFilePreamble + `def silently_close(f: mutable File[Open]&):
	f.is_open <- false
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "strict_balance_bad.elisa", src)
	if !strings.Contains(allDiagnostics(result), "must be left in its declared state") {
		t.Fatalf("an undeclared state change on a borrowed param must error, got: %s", allDiagnostics(result))
	}
}

// Declaring the transition makes a state-changing method legal again.
func TestStrictBalanceDeclaredChangeIsAllowed(t *testing.T) {
	src := strictBalanceFilePreamble + `def do_close(f: mutable File[Open]&) ensures f => Closed:
	f.is_open <- false
`
	result := analyzeFunctionAnalysisTestSource(t, "strict_balance_declared.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a declared state transition must be allowed, got: %v", errs)
	}
}

// Soundness: an EXTERN's native body cannot be verified, so it gets NO implicit preserve — a caller
// must keep widening a borrowed stateful argument across the extern call (here the post-call use as
// File[Open] fails because the state was conservatively widened).
func TestStrictBalanceExternDoesNotImplicitlyPreserve(t *testing.T) {
	src := strictBalanceFilePreamble + `extern native_touch(f: mutable File[Open]&) -> void

def after_extern(f: mutable File[Open]&) -> i64 ensures f => Open:
	native_touch(f)
	return read_after(f)

def read_after(f: mutable File[Open]&) -> i64:
	return 1
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "strict_balance_extern.elisa", src)
	// The extern widened f, so neither the `ensures f => Open` nor the `read_after(f)` (which requires
	// Open) can be satisfied — proving the extern did NOT get an unsound implicit preserve.
	if len(result.Errors()) == 0 {
		t.Fatalf("an extern must not implicitly preserve a borrowed state; the widened use should error, got none")
	}
}

// The legacy mutable-borrow spelling (`mutable f: T&`) is enforced too, not just the canonical
// `f: mutable T&` — mutability is detected in either position.
func TestStrictBalanceLegacyMutableSpelling(t *testing.T) {
	src := strictBalanceFilePreamble + `def legacy_close(mutable f: File[Open]&):
	f.is_open <- false
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "strict_balance_legacy.elisa", src)
	if !strings.Contains(allDiagnostics(result), "must be left in its declared state") {
		t.Fatalf("strict balance must apply to the legacy `mutable f:` spelling too, got: %s", allDiagnostics(result))
	}
}
