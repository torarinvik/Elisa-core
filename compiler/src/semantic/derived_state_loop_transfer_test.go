//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

const derivedLoopPlayer = `struct Player[state Alive | Dead]:
	health: mutable i64
	tag: mutable i64
	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

`

func TestDerivedLoopRejectsLaterIterationCopy(t *testing.T) {
	for _, body := range []string{
		"while again:\n\t\tcopy: Player[Alive] = p{tag = 1}\n\t\tp.health <- 0",
		"while again:\n\t\tcopy: Player[Alive] = p{tag = 1}\n\t\tp.health <- 0\n\t\tcontinue",
		"for index in 0..<3 |p|:\n\t\tcopy: Player[Alive] = p{tag = 1}\n\t\tp.health <- 0",
		"while again:\n\t\tcopy: Player[Alive] = p{tag = 1}\n\t\tif again:\n\t\t\tp.health <- 0",
		"while again:\n\t\thealth <- 0\n\t\tcopy: Player[Alive] = p{tag = 1}",
	} {
		t.Run(body, func(t *testing.T) {
			src := derivedLoopPlayer + "def invalid(p: mutable Player[Alive], again: bool, health: mutable i64&) -> Player[Dead]:\n\t" + body + "\n\treturn p{health = 0}\n"
			result := analyzeDerivedStatePrecision(t, "later_iteration_copy.elisa", src)
			if !strings.Contains(allDiagnostics(result), "later loop iteration expects Player[Alive]") {
				t.Fatalf("later-iteration source was not rejected by current-state transfer: %s", allDiagnostics(result))
			}
		})
	}
}

func TestDerivedLoopAcceptsCurrentStateCopies(t *testing.T) {
	for _, body := range []string{
		"for index in 0..<3 |p|:\n\t\tp.health <- 0\n\t\tcopy: Player[Dead] = p{tag = 1}",
		"for index in 0..=0 |p|:\n\t\tcopy: Player[Alive] = p{tag = 1}\n\t\tp.health <- 0",
		"while again:\n\t\tcopy: Player[Alive] = p{tag = 1}\n\t\tp.health <- 0\n\t\tbreak",
		"while again:\n\t\tp.health <- 0\n\t\tcopy: Player[Dead] = p{tag = 1}",
	} {
		t.Run(body, func(t *testing.T) {
			src := derivedLoopPlayer + "def valid(p: mutable Player[Alive], again: bool) -> Player[Dead]:\n\t" + body + "\n\treturn p{health = 0}\n"
			result := analyzeDerivedStatePrecision(t, "current_iteration_copy.elisa", src)
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("valid current-state copy rejected: %v", errs)
			}
		})
	}
}

func TestDerivedLoopProvenNonemptyExit(t *testing.T) {
	for _, header := range []string{"0..<3", "0..=0"} {
		t.Run(header, func(t *testing.T) {
			src := derivedLoopPlayer + "def valid(p: mutable Player[Alive]) -> Player[Dead]:\n\tfor index in " + header + " |p|:\n\t\tp.health <- 0\n\treturn p{tag = 1}\n"
			result := analyzeDerivedStatePrecision(t, "nonempty_loop_exit.elisa", src)
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("nonempty loop retained an impossible zero-iteration entry: %v", errs)
			}
		})
	}
}
