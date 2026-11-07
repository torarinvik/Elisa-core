package semantic

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func referenceSMTPreambleContext(text string) string {
	var out strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(line, "(declare-") || strings.HasPrefix(line, "(define-fun ") || strings.HasPrefix(line, ";") {
			continue
		}
		seen := map[string]bool{}
		scan := smtTokenScanner{text: line}
		for tok, ok := scan.next(); ok; tok, ok = scan.next() {
			if !smtOperatorOrNumeral(tok) {
				seen[tok] = true
			}
		}
		if len(seen) >= 2 {
			out.WriteString(line)
		}
	}
	return out.String()
}

func TestSMTPreambleContextMatchesReference(t *testing.T) {
	r := rand.New(rand.NewSource(8013))
	words := []string{"x", "y", "v_1", "assert", "and", "1", "42", "\r", "|quoted|", "x:y", ";comment", ""}
	for trial := 0; trial < 2000; trial++ {
		var input strings.Builder
		for n := r.Intn(15); n > 0; n-- {
			switch r.Intn(5) {
			case 0:
				fmt.Fprintf(&input, "(declare-const %s Int)\n", words[r.Intn(len(words))])
			case 1:
				fmt.Fprintf(&input, "; %s\n", words[r.Intn(len(words))])
			default:
				fmt.Fprintf(&input, "(assert (= %s %s %s))\n", words[r.Intn(len(words))], words[r.Intn(len(words))], words[r.Intn(len(words))])
			}
		}
		if trial%7 == 0 {
			input.WriteString("(assert (= x y))")
		}
		got, want := smtPreambleContext(input.String()), referenceSMTPreambleContext(input.String())
		if got != want {
			t.Fatalf("trial %d: got %q want %q", trial, got, want)
		}
	}
}
