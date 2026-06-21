package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The reusable elisacore_std/decode module (docs/94) is consumed by decode_consumer_demo.elisa: the
// consumer builds its own refinement types from the module's laws and decodes with the module's
// verified field-extract and sign-extend primitives. This asserts the firewall holds ACROSS the
// include boundary — every cross-module refinement (a `bits5` return entailing the consumer's
// RegIndex, a `sx12` signed range, a refined-index bounds elision) discharges under -strict — and
// that the decoded values are bit-exact at runtime.
func TestRunCLIDecodeModuleConsumer(t *testing.T) {
	t.Parallel()
	fixturePath := filepath.Join(repoRootFromMainTest(t), "compiler", "decode_consumer_demo.elisa")

	// 1. Every cross-module refinement proves statically (a clean -strict exit, no fallbacks).
	var so, se bytes.Buffer
	if code := runCLI([]string{"-strict", "-emit", "semantic", fixturePath}, &so, &se); code != 0 {
		t.Fatalf("decode-module consumer did not fully prove under -strict (exit %d):\nstderr:\n%s", code, se.String())
	}
	if strings.Contains(se.String(), "could not be proven") {
		t.Fatalf("a cross-module decode refinement fell back to a runtime check:\n%s", se.String())
	}

	// 2. The decoded values are bit-exact at runtime.
	var rso, rse bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &rso, &rse); code != 0 {
		t.Fatalf("decode-module consumer tests failed (exit %d):\nstdout:\n%s\nstderr:\n%s", code, rso.String(), rse.String())
	}
	if !strings.Contains(rso.String(), "passed=2") {
		t.Fatalf("expected both consumer tests to pass, got:\n%s", rso.String())
	}
}
