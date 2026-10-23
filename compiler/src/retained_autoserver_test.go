package main

import "testing"

func TestRequestGeneratesCode(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"-emit", "iface", "a.elisa"}, false},
		{[]string{"-emit", "semantic", "a.elisa"}, false},
		{[]string{"-emit", "llvm", "a.elisa"}, true},
		{[]string{"-emit", "obj", "-o", "a.o", "a.elisa"}, true},
		{[]string{"-o", "a", "a.elisa"}, true},
	}
	for _, c := range cases {
		if got := requestGeneratesCode(c.args); got != c.want {
			t.Errorf("requestGeneratesCode(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestAutoRetainedSocketOptOut(t *testing.T) {
	t.Setenv("ELISACORE_RETAINED_SERVER", "")
	if s := autoRetainedSocket([]string{"-emit", "iface", "a.elisa"}); s != "" {
		t.Errorf("explicit empty server env should compile locally, got %q", s)
	}
}
