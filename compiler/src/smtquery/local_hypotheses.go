package smtquery

import "strings"

// SliceImmutableLocalHypotheses keeps only the immutable-local defining assertions (the block
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
func SliceImmutableLocalHypotheses(localHyps, context string) string {
	if localHyps == "" {
		return ""
	}
	type block struct {
		sym         string
		first, last int
	}
	type hypothesisLine struct {
		text string
		next int
	}
	lineCount := strings.Count(localHyps, "\n") + 1
	blocks := make([]block, 0, lineCount)
	lines := make([]hypothesisLine, 0, lineCount)
	index := make(map[string]int, lineCount)
	for rest := localHyps; rest != ""; {
		line := rest
		if end := strings.IndexByte(rest, '\n'); end >= 0 {
			line, rest = rest[:end+1], rest[end+1:]
		} else {
			rest = ""
		}
		sym, ok := LocalHypothesisSymbol(line)
		if !ok {
			return localHyps
		}
		next := len(lines)
		lines = append(lines, hypothesisLine{text: line, next: -1})
		if i, seen := index[sym]; seen {
			lines[blocks[i].last].next = next
			blocks[i].last = next
		} else {
			index[sym] = len(blocks)
			blocks = append(blocks, block{sym: sym, first: next, last: next})
		}
	}
	// Only block symbols can make another block reachable. Queue each one
	// once instead of repeatedly scanning every block and retaining all SMT tokens.
	keep := make([]bool, len(blocks))
	queue := make([]int, 0, len(blocks))
	enqueue := func(text string) {
		offset := 0
		for tok, next, ok := NextSimpleToken(text, offset); ok; tok, next, ok = NextSimpleToken(text, offset) {
			offset = next
			// LocalHypothesisSymbol admits only v_ and b_ names; operators,
			// literals and other symbols cannot appear in the block index.
			if len(tok) < 2 || tok[1] != '_' || (tok[0] != 'v' && tok[0] != 'b') {
				continue
			}
			if i, found := index[tok]; found && !keep[i] {
				keep[i] = true
				queue = append(queue, i)
			}
		}
	}
	enqueue(context)
	for next := 0; next < len(queue); next++ {
		for i := blocks[queue[next]].first; i >= 0; i = lines[i].next {
			enqueue(lines[i].text)
		}
	}
	var b strings.Builder
	for i, blk := range blocks {
		if keep[i] {
			for j := blk.first; j >= 0; j = lines[j].next {
				b.WriteString(lines[j].text)
			}
		}
	}
	return b.String()
}

// LocalHypothesisSymbol returns the local symbol an immutable-local hypothesis line constrains: the
// first operand of `(assert (= v_x …))`, `(assert (>= v_x 0))` or `(assert (< v_x …))`.
func LocalHypothesisSymbol(line string) (string, bool) {
	for _, prefix := range []string{"(assert (= ", "(assert (>= ", "(assert (< "} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			end := strings.IndexAny(rest, " ()\n")
			if end <= 0 {
				return "", false
			}
			sym := rest[:end]
			if strings.HasPrefix(prefix, "(assert (= ") && strings.HasPrefix(sym, "b_") {
				return sym, true
			}
			if !strings.HasPrefix(sym, "v_") {
				return "", false
			}
			return sym, true
		}
	}
	return "", false
}

// NextSimpleToken scans the simple generated-query token format; its delimiters
// deliberately differ from the full SMT-LIB atom scanner.
func NextSimpleToken(text string, offset int) (string, int, bool) {
	for offset < len(text) && simpleTokenDelimiter[text[offset]] {
		offset++
	}
	start := offset
	for offset < len(text) && !simpleTokenDelimiter[text[offset]] {
		offset++
	}
	if start == offset {
		return "", offset, false
	}
	return text[start:offset], offset, true
}

var simpleTokenDelimiter = [256]bool{' ': true, '(': true, ')': true, '\n': true, '\t': true}

// AppendPreambleContext appends only facts that can connect multiple symbols.
func AppendPreambleContext(out []byte, preamble string) []byte {
	for rest := preamble; rest != ""; {
		line := rest
		if end := strings.IndexByte(rest, '\n'); end >= 0 {
			line, rest = rest[:end+1], rest[end+1:]
		} else {
			rest = ""
		}
		if strings.HasPrefix(line, "(declare-") || strings.HasPrefix(line, "(define-fun ") || strings.HasPrefix(line, ";") {
			continue
		}
		first := ""
		offset := 0
		for tok, next, ok := NextSimpleToken(line, offset); ok; tok, next, ok = NextSimpleToken(line, offset) {
			offset = next
			if OperatorOrNumeral(tok) {
				continue
			}
			if first == "" {
				first = tok
				continue
			}
			if tok != first {
				out = append(out, line...)
				break
			}
		}
	}
	return out
}

func OperatorOrNumeral(tok string) bool {
	switch tok {
	case "assert", "=", ">=", "<=", "<", ">", "-", "+", "*", "not", "and", "or":
		return true
	}
	return tok != "" && tok[0] >= '0' && tok[0] <= '9'
}
