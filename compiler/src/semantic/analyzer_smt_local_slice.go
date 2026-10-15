package semantic

import "strings"

// sliceImmutableLocalHypotheses keeps only the immutable-local defining assertions (the block
// smtImmutableLocalHypotheses emits) whose local is reachable from `context`: the obligation and every
// other hypothesis. A local is reachable when its symbol occurs in the context or in the definition of
// another reachable local.
//
// Dropping the rest loses no proof. Each dropped block constrains only its own local symbol (`v = t`,
// plus `0 <= v < C` for a modulo local), and that symbol occurs nowhere else in the query. Any model of
// the remaining query therefore extends to the dropped equations by choosing v = t. Removing
// assumptions is sound in any case, so the slice can never admit a false proof; at worst (a dropped
// local's own type bound implied something about its definition's operands) it declines a proof. The win is CPU: a local
// such as `next_state = (state ^ byte) * PRIME` in a recursive fold costs z3 a 64-bit multiply on every
// goal in its scope, even goals about an index.
func sliceImmutableLocalHypotheses(localHyps, context string) string {
	if localHyps == "" {
		return ""
	}
	type block struct {
		sym   string
		lines []string
	}
	var blocks []block
	index := map[string]int{}
	for _, line := range strings.SplitAfter(localHyps, "\n") {
		if line == "" {
			continue
		}
		sym, ok := localHypothesisSymbol(line)
		if !ok {
			return localHyps // unexpected shape: keep everything
		}
		if i, seen := index[sym]; seen {
			blocks[i].lines = append(blocks[i].lines, line)
			continue
		}
		index[sym] = len(blocks)
		blocks = append(blocks, block{sym: sym, lines: []string{line}})
	}
	reached := map[string]bool{}
	for _, tok := range smtTokens(context) {
		reached[tok] = true
	}
	keep := make([]bool, len(blocks))
	for changed := true; changed; {
		changed = false
		for i, blk := range blocks {
			if keep[i] || !reached[blk.sym] {
				continue
			}
			keep[i] = true
			changed = true
			for _, line := range blk.lines {
				for _, tok := range smtTokens(line) {
					reached[tok] = true
				}
			}
		}
	}
	var b strings.Builder
	for i, blk := range blocks {
		if keep[i] {
			for _, line := range blk.lines {
				b.WriteString(line)
			}
		}
	}
	return b.String()
}

// localHypothesisSymbol returns the local symbol an immutable-local hypothesis line constrains: the
// first operand of `(assert (= v_x …))`, `(assert (>= v_x 0))` or `(assert (< v_x …))`.
func localHypothesisSymbol(line string) (string, bool) {
	for _, prefix := range []string{"(assert (= ", "(assert (>= ", "(assert (< "} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			end := strings.IndexAny(rest, " ()\n")
			if end <= 0 {
				return "", false
			}
			sym := rest[:end]
			if !strings.HasPrefix(sym, "v_") {
				return "", false
			}
			return sym, true
		}
	}
	return "", false
}

// smtPreambleContext is the part of a fact preamble that can CONNECT symbols: declarations and
// single-symbol bound assertions (`(assert (< v_x 256))`) only constrain one symbol, so they must not
// make every declared local look reachable.
func smtPreambleContext(preamble string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(preamble, "\n") {
		if strings.HasPrefix(line, "(declare-") || strings.HasPrefix(line, ";") {
			continue
		}
		symbols := 0
		seen := map[string]bool{}
		for _, tok := range smtTokens(line) {
			if smtOperatorOrNumeral(tok) || seen[tok] {
				continue
			}
			seen[tok] = true
			symbols++
		}
		if symbols >= 2 {
			b.WriteString(line)
		}
	}
	return b.String()
}

func smtOperatorOrNumeral(tok string) bool {
	switch tok {
	case "assert", "=", ">=", "<=", "<", ">", "-", "+", "*", "not", "and", "or":
		return true
	}
	return tok != "" && tok[0] >= '0' && tok[0] <= '9'
}

// smtTokens splits SMT-LIB text into its atoms (symbols, numerals, operators).
func smtTokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == '\n' || r == '\t'
	})
}
