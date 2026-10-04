package semantic

import (
	"strings"
	"testing"
	"time"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// A cyclic `is` chain used to send computeRecursiveEnumSet's descendant walk into an endless
// append loop (the compiler died out of memory before any diagnostic). Found by FuzzChecker.
// The walk must terminate so the hierarchy checker can report the cycle.
func TestEnumRefinementCycleTerminatesWithDiagnostic(t *testing.T) {
	cases := map[string]string{
		"self":   "enum Node is Node:\n    Add(x: i32)\n\ndef main() -> i32:\n    return 0\n",
		"mutual": "enum A is B:\n    X(x: i32)\nenum B is A:\n    Y(y: i32)\n\ndef main() -> i32:\n    return 0\n",
	}
	want := map[string]string{"self": `enum "Node" cannot refine itself`, "mutual": "refinement cycle"}
	for name, src := range cases {
		done := make(chan string, 1)
		go func() {
			p := parser.New(lexer.New("cycle.elisa", []byte(src)).Tokenize())
			file := p.ParseFile("cycle.elisa")
			if errs := p.Errors(); len(errs) != 0 {
				done <- "parse: " + strings.Join(errs, "\n")
				return
			}
			done <- strings.Join(Analyze(file).Errors(), "\n")
		}()
		select {
		case got := <-done:
			if !strings.Contains(got, want[name]) {
				t.Fatalf("%s: expected %q, got:\n%s", name, want[name], got)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("%s: analysis did not terminate", name)
		}
	}
}
