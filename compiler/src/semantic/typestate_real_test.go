//go:build cgo

package semantic

// typestate_real_test.go — end-to-end audit tests for docs/113.
//
// Proves that the following properties actually hold today (as of the
// docs/111 S0 + named-state substrate work):
//
//   1. Legal state sequences compile with no errors.
//   2. Illegal state operations (wrong pre-state) are hard compile errors.
//   3. Phantom-erasure holds: the __typestate field is Phantom=true and absent
//      from the non-phantom layout, matching a plain struct with the same real fields.
//   4. terminal: keyword causes a non-terminal exit state to be an error.
//   5. linear typestate requires exactly-once consumption.
//   6. Method-call syntax and free-function syntax are both accepted.
//
// These tests supplement the existing typestate_protocol_runtime_test.go and
// typestate_state_test.go but focus on the boundary between what docs/113 claims
// is "design" vs what is actually enforced today.

import (
	"strings"
	"testing"
)

// ── helper constants ─────────────────────────────────────────────────────────

const tsLockProtocol = `typestate Lock:
	id: mutable i64
	states: Unlocked, Locked
	transition acquire: Unlocked -> Locked
	transition release: Locked -> Unlocked

`

// ── 1. Legal sequences compile ───────────────────────────────────────────────

// TestTSRealLegalAcquireRelease proves a full acquire→release round-trip
// compiles without errors, using free-function transition syntax.
func TestTSRealLegalAcquireRelease(t *testing.T) {
	src := tsLockProtocol + `def use_lock(l: mutable Lock[Unlocked]&) ensures l => Unlocked:
	acquire(l)
	release(l)
`
	result := analyzeFunctionAnalysisTestSource(t, "ts_lock_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("legal acquire→release must compile clean, got: %v", errs)
	}
}

// TestTSRealMethodCallAcquireRelease proves the same round-trip via method-call
// syntax (receiver.transition()).
func TestTSRealMethodCallAcquireRelease(t *testing.T) {
	src := tsLockProtocol + `def use_lock_method(l: mutable Lock[Unlocked]&) ensures l => Unlocked:
	l.acquire()
	l.release()
`
	result := analyzeFunctionAnalysisTestSource(t, "ts_lock_method_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("method-call acquire→release must compile clean, got: %v", errs)
	}
}

// TestTSRealConstructorInInitialState proves that Socket.new() (or Lock.new())
// returns a value whose state matches the initial state declared in the typestate.
func TestTSRealConstructorInInitialState(t *testing.T) {
	src := tsLockProtocol + `def make() -> Lock[Unlocked]:
	return Lock.new(id: 0)
`
	result := analyzeFunctionAnalysisTestSource(t, "ts_lock_constructor.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("typestate constructor must produce a value in the initial state, got: %v", errs)
	}
}

// ── 2. Illegal operations are hard errors ────────────────────────────────────

// TestTSRealReleaseOnUnlocked proves that calling release() on an Unlocked lock
// (which requires Locked) is a compile error.
func TestTSRealReleaseOnUnlocked(t *testing.T) {
	src := tsLockProtocol + `def bad(l: mutable Lock[Unlocked]&):
	release(l)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ts_lock_bad_release.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("release() on Unlocked lock must be a typestate error")
	}
}

// TestTSRealDoubleAcquireIsError proves double acquire (acquire then acquire again)
// is a compile error — after the first acquire the lock is Locked, and acquire
// requires Unlocked.
func TestTSRealDoubleAcquireIsError(t *testing.T) {
	src := tsLockProtocol + `def double_acquire(l: mutable Lock[Unlocked]&):
	acquire(l)
	acquire(l)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ts_lock_double_acquire.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("double acquire must be a typestate error (second acquire expects Unlocked but l is Locked)")
	}
}

// TestTSRealReadAfterClose proves the "read after close" anti-pattern — the
// canonical motivating example from docs/113 — is rejected.
const tsFileProtocol = `typestate File:
	fd: mutable i64
	states: Closed, Open
	transition open_file: Closed -> Open
	transition read_file: Open -> Open returns i64
	transition close_file: Open -> Closed

`

func TestTSRealReadAfterClose(t *testing.T) {
	src := tsFileProtocol + `def bad_read_after_close(f: mutable File[Closed]&):
	open_file(f)
	close_file(f)
	read_file(f)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ts_file_read_after_close.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("read_file after close_file must be a typestate error (f is Closed, read_file requires Open)")
	}
}

// TestTSRealLegalReadBeforeClose proves a legal open→read→close sequence compiles.
func TestTSRealLegalReadBeforeClose(t *testing.T) {
	src := tsFileProtocol + `def ok_read(f: mutable File[Closed]&) ensures f => Closed:
	open_file(f)
	read_file(f)
	close_file(f)
`
	result := analyzeFunctionAnalysisTestSource(t, "ts_file_legal.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("open→read→close must compile clean, got: %v", errs)
	}
}

// ── 3. Phantom erasure ───────────────────────────────────────────────────────

// TestTSRealPhantomErasureHoldsViaTypestate verifies that a typestate-declared
// type (using the `typestate` keyword, not hand-written `struct`) has its
// __typestate field marked Phantom=true in the analyzed StructType.
// This strengthens the existing typestate_state_test.go tests by checking a
// typestate-keyword-declared type rather than a manually crafted struct.
func TestTSRealPhantomErasureHoldsViaTypestate(t *testing.T) {
	src := tsLockProtocol
	result := analyzeTreeTestSource(t, "ts_erasure.elisa", src)

	var st *StructType
	for _, typ := range result.NamedTypes {
		if s, ok := typ.(*StructType); ok && s.Name == "Lock" {
			st = s
			break
		}
	}
	if st == nil {
		t.Fatalf("Lock struct not found after analysis")
	}

	// Must be marked as typestate-bearing.
	if !st.HasTypestate {
		t.Errorf("Lock should have HasTypestate=true")
	}

	// TypestateStateField must be set.
	if st.TypestateStateField == "" {
		t.Errorf("Lock.TypestateStateField should be non-empty")
	}

	// The state field must be Phantom — zero runtime layout impact.
	stateFieldName := st.TypestateStateField
	sf, ok := st.Fields[stateFieldName]
	if !ok {
		t.Fatalf("state field %q not found in Lock.Fields", stateFieldName)
	}
	if !sf.Phantom {
		t.Errorf("state field %q must be Phantom=true (zero runtime cost), got Phantom=%v", stateFieldName, sf.Phantom)
	}

	// Non-phantom fields must equal the real declared fields (here: just `id`).
	var nonPhantomNames []string
	for name, f := range st.Fields {
		if !f.Phantom {
			nonPhantomNames = append(nonPhantomNames, name)
		}
	}
	if len(nonPhantomNames) != 1 || nonPhantomNames[0] != "id" {
		t.Errorf("expected exactly one non-phantom field 'id', got: %v", nonPhantomNames)
	}
}

// TestTSRealPhantomLayoutMatchesPlainStruct verifies that a typestate struct and
// a plain struct with the same real fields have the same non-phantom field set
// — confirming zero runtime overhead.
func TestTSRealPhantomLayoutMatchesPlainStruct(t *testing.T) {
	src := `typestate MyConn:
	host: cstr
	port: u16
	states: Closed, Open
	transition dial: Closed -> Open
	transition hang_up: Open -> Closed

struct MyConnPlain:
	host: cstr
	port: u16
`
	result := analyzeTreeTestSource(t, "ts_layout_parity.elisa", src)

	var tsStruct, plainStruct *StructType
	for _, typ := range result.NamedTypes {
		s, ok := typ.(*StructType)
		if !ok {
			continue
		}
		if s.Name == "MyConn" {
			tsStruct = s
		} else if s.Name == "MyConnPlain" {
			plainStruct = s
		}
	}
	if tsStruct == nil || plainStruct == nil {
		t.Fatalf("could not find both MyConn and MyConnPlain after analysis")
	}

	nonPhantomOf := func(s *StructType) map[string]bool {
		m := map[string]bool{}
		for name, f := range s.Fields {
			if !f.Phantom {
				m[name] = true
			}
		}
		return m
	}

	tsNP := nonPhantomOf(tsStruct)
	plainNP := nonPhantomOf(plainStruct)

	if len(tsNP) != len(plainNP) {
		t.Errorf("non-phantom field count: typestate=%d plain=%d", len(tsNP), len(plainNP))
	}
	for name := range plainNP {
		if !tsNP[name] {
			t.Errorf("plain field %q is not present as non-phantom in typestate struct", name)
		}
	}
}

// ── 4. Terminal-state enforcement ────────────────────────────────────────────

const tsTerminalConn = `typestate Conn:
	fd: i64
	states: Closed, Open
	terminal: Closed
	transition connect(addr: i64): Closed -> Open
	transition disconnect: Open -> Closed

`

// TestTSRealTerminalLeakIsError proves that a Conn left in Open state at scope
// exit is a compile error when Closed is declared terminal.
func TestTSRealTerminalLeakIsError(t *testing.T) {
	src := tsTerminalConn + `def bad(addr: i64) -> void:
	c: mutable Conn[Closed] = Conn.new(fd: 0)
	connect(c, addr)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ts_terminal_leak.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "non-terminal state") && !strings.Contains(all, "terminal") {
		t.Fatalf("Conn left in Open state must be a terminal-leak error, got:\n%s", all)
	}
}

// TestTSRealTerminalReturnedToTerminalPasses proves the complementary happy path.
func TestTSRealTerminalReturnedToTerminalPasses(t *testing.T) {
	src := tsTerminalConn + `def ok(addr: i64) -> void:
	c: mutable Conn[Closed] = Conn.new(fd: 0)
	connect(c, addr)
	disconnect(c)
`
	result := analyzeFunctionAnalysisTestSource(t, "ts_terminal_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("Conn returned to terminal Closed must compile, got: %v", errs)
	}
}

// ── 5. Linear typestate ──────────────────────────────────────────────────────

const tsLinearToken = `linear typestate Token:
	id: i64
	states: Fresh, Used
	transition redeem: Fresh -> Used

`

// TestTSRealLinearLeakIsCaughtAtScopeExit proves that a linear typestate value
// that is never consumed is reported as leaked.
func TestTSRealLinearLeakIsCaughtAtScopeExit(t *testing.T) {
	src := tsLinearToken + `def leaky() -> void:
	t = Token.new(id: 42)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ts_linear_leak2.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "must be consumed before scope exit") {
		t.Fatalf("linear typestate leak must be an error, got:\n%s", all)
	}
}

// TestTSRealLinearConsumedOnce proves that a properly consumed linear typestate
// token compiles clean. The consumer transitions to Used and declares ensures so
// the borrow-back postcondition is satisfied; the caller's local t is left Used
// at scope exit which satisfies the linear discharge (terminal state).
func TestTSRealLinearConsumedOnce(t *testing.T) {
	src := tsLinearToken + `def discharge(t: mutable Token[Fresh]&) ensures t => Used:
	redeem(t)

def ok() -> void:
	t: mutable Token[Fresh] = Token.new(id: 1)
	discharge(t)
`
	result := analyzeFunctionAnalysisTestSource(t, "ts_linear_ok2.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("linear typestate consumed once (via ensures) must compile, got: %v", errs)
	}
}

// TestTSRealLinearDoubleRedeemIsError proves that calling the transition twice on
// a linear typestate value is caught.
func TestTSRealLinearDoubleRedeemIsError(t *testing.T) {
	src := tsLinearToken + `def bad() -> void:
	t: mutable Token[Fresh] = Token.new(id: 7)
	redeem(t)
	redeem(t)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "ts_linear_double2.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if len(result.Errors()) == 0 {
		t.Fatalf("double redeem of linear typestate must be an error, got no errors")
	}
	if !strings.Contains(all, "consumed") && !strings.Contains(all, "cannot be used") {
		t.Fatalf("error message should mention consumption, got:\n%s", all)
	}
}
