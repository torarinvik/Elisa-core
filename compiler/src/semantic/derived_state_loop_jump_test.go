//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestDerivedStateLoopJumpCarriesMutation(t *testing.T) {
	for _, body := range []string{
		"while again:\n\t\tp.health <- 0\n\t\tbreak",
		"while again:\n\t\tp.health <- 0\n\t\tcontinue",
		"for index in 0..<count |p|:\n\t\tp.health <- 0\n\t\tbreak",
		"for index in 0..<count |p|:\n\t\tp.health <- 0\n\t\tcontinue",
		"for item in items |p|:\n\t\tp.health <- 0\n\t\tbreak",
		"for item in items |p|:\n\t\tp.health <- 0\n\t\tcontinue",
		"while again:\n\t\tif again:\n\t\t\tp.health <- 0\n\t\t\tbreak\n\t\tcontinue",
		"while again:\n\t\tif again:\n\t\t\tcontinue\n\t\tp.health <- 0\n\t\tbreak",
	} {
		t.Run(body, func(t *testing.T) {
			src := derivedStatePlayerPreamble + "def invalid(p: mutable Player[Alive], again: bool, count: i64, items: darray[i64]&) -> Player[Alive]:\n\t" + body + "\n\treturn p\n"
			result := analyzeDerivedStatePrecision(t, "loop_jump_state.elisa", src)
			if diagnostics := allDiagnostics(result); !strings.Contains(diagnostics, "return type expects Player[Alive], got Player[Alive | Dead]") {
				t.Fatalf("jump discarded the state-changing write: %s", diagnostics)
			}
		})
	}
}

func TestDerivedStateLoopJumpPreservesUnchangedEntry(t *testing.T) {
	for _, body := range []string{
		"while again:\n\t\tbreak",
		"while again:\n\t\tcontinue",
		"for index in 0..<count |p|:\n\t\tbreak",
		"for index in 0..<count |p|:\n\t\tcontinue",
		"for item in items |p|:\n\t\tbreak",
		"for item in items |p|:\n\t\tcontinue",
		"while false:\n\t\tp.health <- 0\n\t\tbreak",
		"for index in 0..<0 |p|:\n\t\tp.health <- 0\n\t\tbreak",
	} {
		t.Run(body, func(t *testing.T) {
			src := derivedStatePlayerPreamble + "def preserve(p: mutable Player[Alive], again: bool, count: i64, items: darray[i64]&) -> Player[Alive]:\n\t" + body + "\n\treturn p\n"
			result := analyzeDerivedStatePrecision(t, "unchanged_loop_jump.elisa", src)
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("jump changed an unmodified entry state: %v", errs)
			}
		})
	}
}
