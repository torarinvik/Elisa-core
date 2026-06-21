package parser

import (
	"testing"

	"elisacore/src/ast"
)

const typestateSocketSrc = `typestate Socket:
	fd: mutable i64
	states: Closed, Connecting, Connected
	transition connect: Closed -> Connecting
	transition established: Connecting -> Connected
	transition close: Connected -> Closed
`

// The `typestate` sugar desugars to a state-bearing struct, one initial-state constructor, and one
// transition function per `transition`, all landing flat at file scope.
func TestParseTypestateDesugarsToStructAndTransitions(t *testing.T) {
	file, errs := parseSourceFile(t, typestateSocketSrc)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	var st *ast.StructDecl
	var fns []*ast.FuncDecl
	for _, d := range file.Decls {
		switch n := d.(type) {
		case *ast.StructDecl:
			st = n
		case *ast.FuncDecl:
			fns = append(fns, n)
		}
	}
	if st == nil {
		t.Fatalf("expected a synthesized StructDecl, got decls: %v", file.Decls)
	}
	if st.Name != "Socket" {
		t.Fatalf("expected struct named Socket, got %q", st.Name)
	}
	if len(st.NamedStateCases) != 3 {
		t.Fatalf("expected 3 named state cases, got %v", st.NamedStateCases)
	}
	if len(st.DerivedStates) != 3 {
		t.Fatalf("expected 3 derive-state clauses, got %d", len(st.DerivedStates))
	}
	if len(fns) != 4 {
		t.Fatalf("expected constructor plus 3 transition functions, got %d", len(fns))
	}
	names := map[string]bool{}
	for _, fn := range fns {
		names[fn.Name] = true
		if fn.Name != "__typestate_Socket_new" && len(fn.Ensures) != 1 {
			t.Fatalf("transition %q must carry exactly one ensures-poststate, got %d", fn.Name, len(fn.Ensures))
		}
	}
	for _, want := range []string{"__typestate_Socket_new", "connect", "established", "close"} {
		if !names[want] {
			t.Fatalf("missing synthesized function %q (got %v)", want, names)
		}
	}
}

func TestParseLinearTypestateDesugarsToLinearStruct(t *testing.T) {
	file, errs := parseSourceFile(t, `linear typestate Ticket:
	id: i64
	states: Fresh, Used
	transition use_it: Fresh -> Used
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	st, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected synthesized StructDecl, got %T", file.Decls[0])
	}
	if !st.Affine || st.Droppable {
		t.Fatalf("linear typestate should lower to non-droppable affine struct, got affine=%v droppable=%v", st.Affine, st.Droppable)
	}
}

func TestParseTypestateConditionalTransitionDesugarsToBoolPoststates(t *testing.T) {
	file, errs := parseSourceFile(t, `typestate Socket:
	fd: mutable i64
	states: Closed, Connecting, Connected
	transition connect: Closed -> Connecting
	transition poll: Connecting -> Connected | Closed
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	var poll *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "poll" {
			poll = fn
			break
		}
	}
	if poll == nil {
		t.Fatalf("missing synthesized poll transition in %#v", file.Decls)
	}
	if poll.ReturnType == nil {
		t.Fatalf("conditional transition must lower to a bool-returning function")
	}
	if len(poll.Ensures) != 2 {
		t.Fatalf("conditional transition must carry two ensures-poststates, got %d", len(poll.Ensures))
	}
	if poll.Ensures[0].Condition.Kind != ast.EnsuresConditionReturnBool || !poll.Ensures[0].Condition.ReturnBool {
		t.Fatalf("first conditional poststate must be return-true, got %#v", poll.Ensures[0].Condition)
	}
	if poll.Ensures[0].Kind != ast.EnsuresKindNamedState || len(poll.Ensures[0].StateCases) != 1 || poll.Ensures[0].StateCases[0] != "Connected" {
		t.Fatalf("true branch must transition to Connected, got %#v", poll.Ensures[0])
	}
	if poll.Ensures[1].Condition.Kind != ast.EnsuresConditionReturnBool || poll.Ensures[1].Condition.ReturnBool {
		t.Fatalf("second conditional poststate must be return-false, got %#v", poll.Ensures[1].Condition)
	}
	if poll.Ensures[1].Kind != ast.EnsuresKindNamedState || len(poll.Ensures[1].StateCases) != 1 || poll.Ensures[1].StateCases[0] != "Closed" {
		t.Fatalf("false branch must transition to Closed, got %#v", poll.Ensures[1])
	}
}

func TestParseTypestateTerminalAndRichTransitionSignatures(t *testing.T) {
	file, errs := parseSourceFile(t, `typestate File:
	states: Closed, Open
	terminal: Closed
	transition open(path: i64): Closed -> Open
	transition read: Open -> Open returns i64
	transition close: Open -> Closed
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	st, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected synthesized StructDecl, got %T", file.Decls[0])
	}
	if len(st.TerminalStateCases) != 1 || st.TerminalStateCases[0] != "Closed" {
		t.Fatalf("expected Closed terminal state, got %#v", st.TerminalStateCases)
	}
	var openFn, readFn *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name {
		case "open":
			openFn = fn
		case "read":
			readFn = fn
		}
	}
	if openFn == nil || len(openFn.Params) != 2 || openFn.Params[1].Name != "path" {
		t.Fatalf("open transition should include self plus path param, got %#v", openFn)
	}
	if readFn == nil || readFn.ReturnType == nil {
		t.Fatalf("read transition should include return type, got %#v", readFn)
	}
	if len(readFn.Ensures) != 1 {
		t.Fatalf("read transition should carry poststate ensure, got %d", len(readFn.Ensures))
	}
}

// An unknown source/target state in a transition is a hard error.
func TestParseTypestateUnknownStateIsError(t *testing.T) {
	_, errs := parseSourceFile(t, `typestate S:
	states: A, B
	transition go: A -> C
`)
	if len(errs) == 0 {
		t.Fatalf("a transition into an undeclared state must be a parse error")
	}
}

func TestParseTypestateAndContractDeclDispatchRemainDistinct(t *testing.T) {
	file, errs := parseSourceFile(t, `typestate Socket:
	fd: mutable i64
	states: Closed, Open
	transition open: Closed -> Open

contract NonNegValue(value: i64):
	requires value >= 0
	ensure result >= value

def keep(value: i64) -> i64:
	uses NonNegValue(value)
	return value
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	var sawSocket, sawTransition, sawContract, sawKeep bool
	for _, decl := range file.Decls {
		fn, isFn := decl.(*ast.FuncDecl)
		if st, ok := decl.(*ast.StructDecl); ok && st.Name == "Socket" {
			sawSocket = true
		}
		if isFn && fn.Name == "open" {
			sawTransition = true
		}
		if isFn && fn.Name == "NonNegValue" && fn.IsContract {
			sawContract = true
		}
		if isFn && fn.Name == "keep" && len(fn.Uses) == 1 {
			sawKeep = true
		}
	}
	if !sawSocket || !sawTransition || !sawContract || !sawKeep {
		t.Fatalf("expected typestate sugar and named contract decls to dispatch distinctly, got %#v", file.Decls)
	}
}
