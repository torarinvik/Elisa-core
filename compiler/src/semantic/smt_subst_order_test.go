package semantic

import (
	"io"
	"os"
	"strings"
	"testing"
)

// Binding a call's substitution walked a Go map: the order named the fresh sview-argument
// symbols (svarg$N) and decided which binding declined first, so the emitted query text (and
// whether a query hit the rlimit budget) changed from run to run. The exact queries sent to the
// solver for repeated analyses of one source must be byte-identical.
func TestSMTSubstitutionBindingIsDeterministic(t *testing.T) {
	src := `
def span_total(a: sview, b: sview, c: sview, d: sview) -> usize:
    requires a.len + b.len + c.len + d.len >= 0
    return a.len.usize() + b.len.usize() + c.len.usize() + d.len.usize()

def caller(text: sview, i: usize, j: usize, k: usize) -> usize:
    requires i < j and j < k and k < text.len.usize()
    return span_total(text[0:i], text[i:j], text[j:k], text[k:text.len.usize()])
`
	captureQueries := func() string {
		saved, savedTrace := os.Stderr, smtQueryTrace
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stderr, smtQueryTrace = w, true
		done := make(chan string)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		analyzeContractStrict(t, "subst_order.elisa", src)
		w.Close()
		os.Stderr, smtQueryTrace = saved, savedTrace
		return <-done
	}
	want := captureQueries()
	if !strings.Contains(want, "@svarg$3") {
		t.Fatalf("fixture no longer binds four sview arguments; queries:\n%s", want)
	}
	for i := 0; i < 24; i++ {
		if got := captureQueries(); got != want {
			t.Fatalf("run %d emitted different solver queries", i)
		}
	}
}
