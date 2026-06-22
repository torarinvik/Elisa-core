package semantic

import "testing"

// P4 — effect/error/frame SUBSET variance on protocol-method conformance (impl ⊆ protocol).
// Clause order follows the grammar: params, `-> ret`, `can[..]`, `ensures`, `changes`.

// EFFECT subset: an impl that uses a capability NOT in the protocol's `can[..]` set is rejected.
func TestImplEffectExtraCapabilityRejected(t *testing.T) {
	src := `
struct Net:
    fd: i64

protocol Sender:
    def send(self: Self, n: i64) -> void can[Atomics.Load]

impl Sender for Net:
    def send(self: Net, n: i64) -> void can[Atomics.Load, Atomics.Store]:
        return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "impl_effect_extra.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "subset of the protocol") {
		t.Fatalf("impl using an extra capability must be rejected, got:\n%s", allDiagnostics(result))
	}
}

// EFFECT subset (positive): an impl using a strict SUBSET of the protocol's capabilities conforms.
func TestImplEffectSubsetOK(t *testing.T) {
	src := `
struct Net:
    fd: i64

protocol Sender:
    def send(self: Self, n: i64) -> void can[Atomics.Load, Atomics.Store]

impl Sender for Net:
    def send(self: Net, n: i64) -> void can[Atomics.Load]:
        return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "impl_effect_subset.elisa", src, AnalyzeOptions{})
	if contains(allDiagnostics(result), "subset of the protocol") {
		t.Fatalf("impl using a subset of capabilities must conform, got:\n%s", allDiagnostics(result))
	}
}

// ERROR subset: an impl that raises an error set NOT in the protocol's `error[..]` set is rejected.
func TestImplErrorExtraTagRejected(t *testing.T) {
	src := `
error NotFound:
    Missing

error Denied:
    NoAccess

struct Disk:
    fd: i64

protocol Reader:
    def read(self: Self) -> i64 error[NotFound]

impl Reader for Disk:
    def read(self: Disk) -> i64 error[NotFound, Denied]:
        raise Denied.NoAccess
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "impl_error_extra.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "subset of the protocol") {
		t.Fatalf("impl raising an extra error must be rejected, got:\n%s", allDiagnostics(result))
	}
}

// ERROR subset (positive): an impl raising a SUBSET of the protocol's error set conforms.
func TestImplErrorSubsetOK(t *testing.T) {
	src := `
error NotFound:
    Missing

error Denied:
    NoAccess

struct Disk:
    fd: i64

protocol Reader:
    def read(self: Self) -> i64 error[NotFound, Denied]

impl Reader for Disk:
    def read(self: Disk) -> i64 error[NotFound]:
        raise NotFound.Missing
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "impl_error_subset.elisa", src, AnalyzeOptions{})
	if contains(allDiagnostics(result), "subset of the protocol") {
		t.Fatalf("impl raising a subset of errors must conform, got:\n%s", allDiagnostics(result))
	}
}

// FRAME subset: an impl `push` that writes a place OUTSIDE the protocol's `changes` frame is rejected.
func TestImplChangesOutsideFrameRejected(t *testing.T) {
	src := `
struct AuditedVec:
    elements: mutable i64
    audit_count: mutable i64

protocol Sink:
    def push(self: mutable Self&, x: i64) -> void changes self.elements

impl Sink for AuditedVec:
    def push(self: mutable AuditedVec&, x: i64) -> void changes self.elements, self.audit_count:
        self.elements <- x
        self.audit_count <- self.audit_count + 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "impl_changes_extra.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the protocol's `changes` frame") {
		t.Fatalf("impl widening the changes frame must be rejected, got:\n%s", allDiagnostics(result))
	}
}

// FRAME subset (positive): an impl whose `changes` frame is a SUBSET of the protocol's conforms.
func TestImplChangesSubsetOK(t *testing.T) {
	src := `
struct AuditedVec:
    elements: mutable i64
    spare: mutable i64

protocol Sink:
    def push(self: mutable Self&, x: i64) -> void changes self.elements, self.spare

impl Sink for AuditedVec:
    def push(self: mutable AuditedVec&, x: i64) -> void changes self.elements:
        self.elements <- x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "impl_changes_subset.elisa", src, AnalyzeOptions{})
	if contains(allDiagnostics(result), "outside the protocol's `changes` frame") {
		t.Fatalf("impl with a subset changes frame must conform, got:\n%s", allDiagnostics(result))
	}
}
