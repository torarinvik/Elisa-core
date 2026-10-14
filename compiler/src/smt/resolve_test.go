package smt

import "testing"

func TestResolveBinaryPrecedence(t *testing.T) {
	saved := DefaultBinary
	defer func() { DefaultBinary = saved }()
	DefaultBinary = "/built/z3"
	t.Setenv("ELISA_Z3", "")
	if got := ResolveBinary(""); got != "/built/z3" {
		t.Fatalf("default: got %q", got)
	}
	t.Setenv("ELISA_Z3", "/env/z3")
	if got := ResolveBinary(""); got != "/env/z3" {
		t.Fatalf("env: got %q", got)
	}
	if got := ResolveBinary("/flag/z3"); got != "/flag/z3" {
		t.Fatalf("explicit: got %q", got)
	}
}

func TestIsZ3ByBasename(t *testing.T) {
	for _, b := range []string{"z3", "/opt/z3/bin/z3", `C:\z3\z3.exe`} {
		if !isZ3(b) {
			t.Fatalf("%q should be z3", b)
		}
	}
	for _, b := range []string{"cvc5", "/opt/z3/bin/yices", "/x/z3-wrapper"} {
		if isZ3(b) {
			t.Fatalf("%q should not be z3", b)
		}
	}
}

// A missing solver must decline (Unknown), never prove.
func TestMissingSolverIsUnknown(t *testing.T) {
	s, err := Open(Options{Binary: "/nonexistent/z3"})
	if err == nil {
		defer s.Close()
		if r, _ := s.Check("(assert false)"); r != Unknown {
			t.Fatalf("missing solver answered %v", r)
		}
	}
}
