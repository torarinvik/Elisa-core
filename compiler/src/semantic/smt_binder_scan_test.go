package semantic

import (
	"math/rand"
	"strings"
	"testing"
)

func smtTermMentionsQuantifierBinderReference(term string) bool {
	// Binders introduced INSIDE the term (`(forall ((q_k Int)) ...)`) are bound there; only a
	// binder with no enclosing quantifier in the term is free. Binder names carry a unique counter
	// suffix, so a name bound anywhere in the term is bound at all of its occurrences.
	bound := map[string]bool{}
	for _, q := range []string{"(forall (", "(exists ("} {
		rest := term
		for {
			i := strings.Index(rest, q)
			if i < 0 {
				break
			}
			rest = rest[i+len(q):]
			for strings.HasPrefix(rest, "(") {
				end := strings.IndexAny(rest[1:], " )")
				if end < 0 {
					break
				}
				bound[rest[1:1+end]] = true
				close := strings.Index(rest, ")")
				if close < 0 {
					break
				}
				rest = strings.TrimLeft(rest[close+1:], " ")
			}
		}
	}
	for _, tok := range strings.FieldsFunc(term, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == '|' || r == ';' || r == '=' || r == ','
	}) {
		if strings.HasPrefix(tok, "q_") && !bound[tok] {
			return true
		}
	}
	return false
}
func TestSMTBinderScanTextSemantics(t *testing.T) {
	cases := []string{"", "q_", "xq_a", "q_a", "(forall ((q_a Int)) (= q_a 1))", "q_a (forall ((q_a Int)) true)", "(exists ((q_a Int) (q_b Int)) (= q_a q_b))", "(forall ((q_a Int)) q_other)", "q_a\tmore", "q_a\nmore", "|q_a|", ";q_a=1,", "πq_a", "q_π", "\xffq_a", "q_\xff", "(forall ((q_a)", "(forall ((q_a Int)) (exists ((q_b Int)) q_c))"}
	for _, s := range cases {
		if got, want := smtTermMentionsQuantifierBinder(s), smtTermMentionsQuantifierBinderReference(s); got != want {
			t.Fatalf("%q:got%v want%v", s, got, want)
		}
	}
}
func TestSMTBinderScanGeneratedDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(19))
	parts := []string{"q_a", "xq_a", "q_b", "q_", "x", "π", "\xff", " ", "(", ")", "|", ";", "=", ",", "\t", "\n", "(forall ((q_a Int)) ", "(exists ((q_b Int)) "}
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		for j, n := 0, rng.Intn(80); j < n; j++ {
			b.WriteString(parts[rng.Intn(len(parts))])
		}
		s := b.String()
		if smtTermMentionsQuantifierBinder(s) != smtTermMentionsQuantifierBinderReference(s) {
			t.Fatalf("differential mismatch %q", s)
		}
	}
}
