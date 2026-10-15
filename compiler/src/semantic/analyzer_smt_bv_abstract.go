package semantic

import (
	"sort"
	"strconv"
	"strings"
)

// Bit-vector abstraction (first pass for int2bv/bv2nat queries).
//
// Every bitwise result reaches the Int side as a `(bv2nat …)` read-back. z3's Int↔BV bridge is
// eager and expensive: on `hash & (capacity - 1) < capacity` the integer core (the AND bound
// lemma, `mask = capacity - 1`, `capacity >= 1`) is unsat in ~2k rlimit, but with the bridge
// present z3 spends its whole budget. smtAbstractBitvectorReads replaces each distinct
// `(bv2nat …)` term by a fresh Int constant everywhere (the guarded AND bound lemmas are carried
// over onto that constant). That is a WEAKENING of the query — any model of the original gives a
// model of the abstraction by assigning each constant its term's value — so an unsat abstraction
// proves the original. A sat or unknown abstraction proves nothing and the original query runs.

const smtBVAbstractPrefix = "bvread$"

// smtAbstractionOnlyMarker ends a hypothesis line that is used ONLY by the abstraction pass (the
// guarded AND bound lemmas). They are valid facts, but inside the full Int↔BV query they make z3
// spend its budget even on goals it previously refuted cheaply, so the full query drops them.
const smtAbstractionOnlyMarker = " ; elisa:abstraction-only"

// smtOnlyAndBitvectorOps reports whether `bvand` is the only bit-vector operator in a query —
// the case the abstraction's AND bound lemma models. Shifts, or/xor and extracts carry semantics
// the abstraction drops (sign-extension ranges, shifted bounds), so their bridged query keeps the
// full session budget.
func smtOnlyAndBitvectorOps(query string) bool {
	for _, op := range []string{"(bvshl ", "(bvlshr ", "(bvashr ", "(bvor ", "(bvxor ", "(bvnot ", "(_ extract", "(bvadd ", "(bvmul ", "(bvsub "} {
		if strings.Contains(query, op) {
			return false
		}
	}
	return strings.Contains(query, "(bvand ")
}

// smtStripAbstractionOnly removes the abstraction-only lines from a query. Dropping valid
// hypotheses only weakens the query, so this never creates a proof.
func smtStripAbstractionOnly(query string) string {
	if !strings.Contains(query, smtAbstractionOnlyMarker) {
		return query
	}
	lines := strings.SplitAfter(query, "\n")
	var b strings.Builder
	for _, line := range lines {
		if strings.HasSuffix(strings.TrimRight(line, "\n"), smtAbstractionOnlyMarker) {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// smtAbstractBitvectorReads returns the query with every bv2nat read-back replaced by a fresh
// constant, or false when the query has none or still mentions bit-vectors afterwards (a term
// shape this rewrite does not cover — e.g. a bitvector-sorted declaration).
func smtAbstractBitvectorReads(query string) (string, bool) {
	if !strings.Contains(query, "(bv2nat ") {
		return "", false
	}
	names := map[string]string{}
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(query[i:], "(bv2nat ")
		if j < 0 {
			b.WriteString(query[i:])
			break
		}
		start := i + j
		end, ok := smtBalancedEnd(query, start)
		if !ok {
			return "", false
		}
		term := query[start:end]
		name, seen := names[term]
		if !seen {
			name = smtBVAbstractPrefix + strconv.Itoa(len(names))
			names[term] = name
		}
		b.WriteString(query[i:start])
		b.WriteString(name)
		i = end
	}
	body := b.String()
	if strings.Contains(body, "int2bv") || strings.Contains(body, "(_ BitVec") || strings.Contains(body, "bv2int") {
		return "", false
	}
	decls := make([]string, 0, len(names))
	for term, name := range names {
		// bv2nat of a W-bit vector lies in [0, 2^W): keep that range on the fresh constant. It
		// holds for the original term, so the abstraction stays a weakening.
		decl := "(declare-const " + name + " Int)\n(assert (<= 0 " + name + "))\n"
		if w, ok := smtBitvectorReadWidth(term); ok {
			decl += "(assert (< " + name + " " + smtPow2(w) + "))\n"
		}
		decls = append(decls, decl)
	}
	sort.Strings(decls)
	return strings.Join(decls, "") + body, true
}

// smtBitvectorReadWidth returns W for a `(bv2nat …)` read-back whose bit-vector is built only
// from `(_ int2bv W)` operands of one width with width-preserving operators. Any width-changing
// operator (extract, concat, extension, repeat) or mixed widths decline: no upper bound is
// asserted then, only the always-valid lower bound 0.
func smtBitvectorReadWidth(term string) (int, bool) {
	for _, op := range []string{"extract", "concat", "zero_extend", "sign_extend", "repeat", "rotate"} {
		if strings.Contains(term, op) {
			return 0, false
		}
	}
	width := -1
	rest := term
	for {
		i := strings.Index(rest, "(_ int2bv ")
		if i < 0 {
			break
		}
		rest = rest[i+len("(_ int2bv "):]
		j := strings.IndexByte(rest, ')')
		if j < 0 {
			return 0, false
		}
		w, err := strconv.Atoi(strings.TrimSpace(rest[:j]))
		if err != nil || w <= 0 || w > 64 || (width >= 0 && w != width) {
			return 0, false
		}
		width = w
	}
	return width, width > 0
}

// smtBalancedEnd returns the index just past the s-expression opening at text[start] ('(').
func smtBalancedEnd(text string, start int) (int, bool) {
	depth := 0
	for k := start; k < len(text); k++ {
		switch text[k] {
		case '|':
			m := strings.IndexByte(text[k+1:], '|')
			if m < 0 {
				return 0, false
			}
			k += m + 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return k + 1, true
			}
		}
	}
	return 0, false
}
