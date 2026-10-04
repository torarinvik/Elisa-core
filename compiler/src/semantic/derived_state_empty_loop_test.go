//go:build cgo

package semantic

import "testing"

func TestDerivedStateEmptyLoopPreservesEntry(t *testing.T) {
	for _, body := range []string{
		"while false:\n\t\tp.health <- 0",
		"for index in 0..<0 |p|:\n\t\tp.health <- 0",
		"for index in 3..<0 |p|:\n\t\tp.health <- 0",
	} {
		t.Run(body, func(t *testing.T) {
			src := derivedStatePlayerPreamble + "def preserve(p: mutable Player[Alive]) -> Player[Alive]:\n\t" + body + "\n\treturn p\n"
			result := analyzeDerivedStatePrecision(t, "empty_loop.elisa", src)
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("unexecuted body changed entry state: %v", errs)
			}
		})
	}
}

func TestDerivedStatePotentiallyExecutedLoopDoesNotPreserveEntry(t *testing.T) {
	for _, body := range []string{
		"while again:\n\t\tp.health <- 0",
		"for index in 0..<count |p|:\n\t\tp.health <- 0",
		"for index in 0..<1 |p|:\n\t\tp.health <- 0",
	} {
		t.Run(body, func(t *testing.T) {
			src := derivedStatePlayerPreamble + "def invalid(p: mutable Player[Alive], again: bool, count: i64) -> Player[Alive]:\n\t" + body + "\n\treturn p\n"
			result := analyzeDerivedStatePrecision(t, "potentially_executed_loop.elisa", src)
			if len(result.Errors()) == 0 {
				t.Fatal("a potentially executed state-changing body preserved Alive")
			}
		})
	}
}
