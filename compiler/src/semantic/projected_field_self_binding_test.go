package semantic

import (
	"strings"
	"testing"
	"time"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// `h = h.r` binds h to a projection of itself. resolveProjectedFieldValueExprAtPath followed
// the binding back into h with one more `.r` step each lap and never returned. Found by
// FuzzChecker (via its hang triage). The analysis must terminate with the ordinary error.
func TestSelfProjectingBindingTerminates(t *testing.T) {
	src := "def d():\n    h = h.r\n"
	done := make(chan string, 1)
	go func() {
		p := parser.New(lexer.New("self_proj.elisa", []byte(src)).Tokenize())
		file := p.ParseFile("self_proj.elisa")
		if errs := p.Errors(); len(errs) != 0 {
			done <- "parse: " + strings.Join(errs, "\n")
			return
		}
		done <- strings.Join(Analyze(file).Errors(), "\n")
	}()
	select {
	case got := <-done:
		if !strings.Contains(got, `undefined identifier "h"`) {
			t.Fatalf("unexpected diagnostics:\n%s", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("analysis of a self-projecting binding did not terminate")
	}
}
