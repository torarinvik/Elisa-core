//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/96: the `typestate` declaration is the sequence-typestate axis. It desugars (in the parser) onto
// the named-state / strict-protocol-balance substrate, so a legal transition sequence compiles and an
// illegal transition (calling a transition out of the wrong state) is a hard error at the call site.

const typestateSocketProtocol = `typestate Socket:
	fd: mutable i64
	states: Closed, Connecting, Connected
	transition connect: Closed -> Connecting
	transition established: Connecting -> Connected
	transition close: Connected -> Closed

`

const typestateFileProtocol = `typestate File:
	fd: mutable i64
	states: Open, Closed
	transition open_it: Closed -> Open
	transition close_it: Open -> Closed

`

const typestateTerminalFileProtocol = `typestate File:
	states: Closed, Open
	terminal: Closed
	transition open(path: i64): Closed -> Open
	transition read: Open -> Open returns i64
	transition close: Open -> Closed

`

// (a) A legal protocol walk Closed -> Connecting -> Connected -> Closed compiles cleanly.
func TestTypestateLegalSequenceCompiles(t *testing.T) {
	src := typestateSocketProtocol + `def use(s: mutable Socket[Closed]&) ensures s => Closed:
	connect(s)
	established(s)
	close(s)
`
	result := analyzeFunctionAnalysisTestSource(t, "typestate_socket_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a legal typestate sequence must compile, got: %v", errs)
	}
}

func TestTypestateMethodCallTransitionsCompile(t *testing.T) {
	src := typestateSocketProtocol + `def use(s: mutable Socket[Closed]&) ensures s => Closed:
	s.connect()
	s.established()
	s.close()
`
	result := analyzeFunctionAnalysisTestSource(t, "typestate_socket_methods_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("method-call typestate transitions must compile, got: %v", errs)
	}
}

func TestTypestateInitialConstructorCompiles(t *testing.T) {
	src := typestateSocketProtocol + `def build() -> Socket[Closed]:
	return Socket.new(fd: 7)
`
	result := analyzeFunctionAnalysisTestSource(t, "typestate_socket_new_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("typestate initial constructor must compile, got: %v", errs)
	}
}

func TestLinearTypestateBindingMustBeConsumed(t *testing.T) {
	src := `linear typestate Ticket:
	id: i64
	states: Fresh, Used
	transition use_it: Fresh -> Used

def consume_ticket(t: Ticket[Fresh]) -> void:
	pass

def ok() -> void:
	t = Ticket.new(id: 1)
	consume_ticket(move t)

def leak() -> void:
	t = Ticket.new(id: 2)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "typestate_linear_consume.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "must be consumed before scope exit") {
		t.Fatalf("linear typestate binding must be consumed exactly once, got:\n%s", all)
	}
}

func TestLinearTypestateTransitionConsumesOnce(t *testing.T) {
	src := `linear typestate Ticket:
	id: i64
	states: Fresh, Used
	transition redeem: Fresh -> Used

def double_spend() -> void:
	t: mutable Ticket[Fresh] = Ticket.new(id: 1)
	redeem(t)
	redeem(t)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "typestate_linear_double_spend.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "cannot be used") || !strings.Contains(all, "consumed") {
		t.Fatalf("second transition of linear typestate must be a consumed-use error, got:\n%s", all)
	}
}

func TestLinearTypestateTransitionLeakIsError(t *testing.T) {
	src := `linear typestate Ticket:
	id: i64
	states: Fresh, Used
	transition redeem: Fresh -> Used

def leak() -> void:
	t: mutable Ticket[Fresh] = Ticket.new(id: 2)
	pass
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "typestate_linear_leak.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "linear value") || !strings.Contains(all, "must be consumed before scope exit") {
		t.Fatalf("unconsumed linear typestate must be a leak error, got:\n%s", all)
	}
}

// (b) An illegal transition — established() requires Connecting but the socket is Closed — is a hard
// error. This is the whole point: illegal operation sequences are compile errors.
func TestTypestateIllegalTransitionIsError(t *testing.T) {
	src := typestateSocketProtocol + `def misuse(s: mutable Socket[Closed]&):
	established(s)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "typestate_socket_bad.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("calling established() on a Closed socket must be a typestate error")
	}
}

// Double-close is impossible: after close() the socket is Closed, and close() requires Connected.
func TestTypestateDoubleCloseIsError(t *testing.T) {
	src := typestateSocketProtocol + `def double(s: mutable Socket[Connected]&):
	close(s)
	close(s)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "typestate_double_close.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("a double close() must be a typestate error")
	}
}

// The worked File (Open -> Closed) example from docs/96 compiles.
func TestTypestateFileProtocolCompiles(t *testing.T) {
	src := typestateFileProtocol + `def round_trip(f: mutable File[Closed]&) ensures f => Closed:
	open_it(f)
	close_it(f)
`
	result := analyzeFunctionAnalysisTestSource(t, "typestate_file_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("the File protocol round trip must compile, got: %v", errs)
	}
}

func TestTypestateTerminalNonTerminalLocalLeaks(t *testing.T) {
	src := typestateTerminalFileProtocol + `def leaky(path: i64) -> void:
	f: mutable File[Closed] = File.new()
	open(f, path)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "typestate_terminal_leak.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "non-terminal state") || !strings.Contains(all, "f") {
		t.Fatalf("typestate value left Open must be a terminal leak error, got:\n%s", all)
	}
}

func TestTypestateTerminalStatePassesAtScopeExit(t *testing.T) {
	src := typestateTerminalFileProtocol + `def ok(path: i64) -> void:
	f: mutable File[Closed] = File.new()
	open(f, path)
	close(f)
`
	result := analyzeFunctionAnalysisTestSource(t, "typestate_terminal_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("typestate value returned to terminal state must compile, got: %v", errs)
	}
}

func TestTypestateTransitionParamsReturnAndPoststate(t *testing.T) {
	src := typestateTerminalFileProtocol + `def read_then_close(path: i64) -> i64:
	f: mutable File[Closed] = File.new()
	open(f, path)
	value = read(f)
	close(f)
	return value

def bad_read(path: i64) -> i64:
	f: mutable File[Closed] = File.new()
	return read(f)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "typestate_transition_signature.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "bad_read") && !strings.Contains(all, "File[Open]") {
		t.Fatalf("read should type-check after open and reject Closed input, got:\n%s", all)
	}
	if strings.Contains(all, "read_then_close") {
		t.Fatalf("read transition with return should type-check in legal sequence, got:\n%s", all)
	}
}
