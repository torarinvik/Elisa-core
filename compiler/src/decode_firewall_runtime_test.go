package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The decode-firewall demo (docs/94, compiler/decode_firewall_demo.elisa) exercises the verified
// bit-extraction pipeline end to end: field masks proving width refinements, an arithmetic-shift
// sign-extension proving a signed immediate range, and a refined register index eliding its bounds
// check. This asserts BOTH that every refinement discharges statically under -strict (the firewall
// holds with no runtime fallback) AND that the decoded values are bit-exact at runtime.
func TestRunCLIDecodeFirewallDemo(t *testing.T) {
	t.Parallel()
	fixturePath := filepath.Join(repoRootFromMainTest(t), "compiler", "decode_firewall_demo.elisa")

	// 1. Every refinement proves statically. Under -strict an unproven obligation is an error, so a
	//    clean exit means the whole firewall discharged — no runtime bounds checks, no fallbacks.
	var so, se bytes.Buffer
	if code := runCLI([]string{"-strict", "-emit", "semantic", fixturePath}, &so, &se); code != 0 {
		t.Fatalf("decode firewall did not fully prove under -strict (exit %d):\nstderr:\n%s", code, se.String())
	}
	if strings.Contains(se.String(), "could not be proven") {
		t.Fatalf("a decode-firewall refinement fell back to a runtime check:\n%s", se.String())
	}

	// 2. The decoded values are bit-exact at runtime.
	var rso, rse bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &rso, &rse); code != 0 {
		t.Fatalf("decode firewall tests failed (exit %d):\nstdout:\n%s\nstderr:\n%s", code, rso.String(), rse.String())
	}
	if !strings.Contains(rso.String(), "passed=2") {
		t.Fatalf("expected both decode-firewall tests to pass, got:\n%s", rso.String())
	}
}
