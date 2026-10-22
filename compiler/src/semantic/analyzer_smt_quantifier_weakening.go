package semantic

import "strings"

// smtWithoutQuantifiedHypotheses retains quantifier-free consequences before a
// quantifier-free goal. This weakens the query: UNSAT still proves the original,
// but SAT or UNKNOWN says nothing and must fall back to the full hypotheses.
// Declarations, definitions, the goal and all subsequent commands stay intact.
func smtWithoutQuantifiedHypotheses(query, goalAssert string) (string, bool) {
	containsQuantifier := func(command string) bool {
		scan := smtAtomScanner{text: command}
		for atom, present := scan.next(); present; atom, present = scan.next() {
			if atom == "forall" || atom == "exists" {
				return true
			}
		}
		return false
	}
	if containsQuantifier(goalAssert) {
		return "", false
	}
	commands, ok := smtSplitTopLevel(query)
	if !ok {
		return "", false
	}
	goalIndex := -1
	for i := len(commands) - 1; i >= 0; i-- {
		if commands[i] == goalAssert {
			goalIndex = i
			break
		}
	}
	if goalIndex < 0 {
		return "", false
	}
	replacements := make([]string, goalIndex)
	drop := make([]bool, goalIndex)
	dropped := false
	for i := 0; i < goalIndex; i++ {
		if smtCommandHead(commands[i]) == "assert" && containsQuantifier(commands[i]) {
			drop[i] = true
			if consequence, ok := smtQuantifierFreeConsequence(commands[i]); ok {
				replacements[i] = consequence
			}
			dropped = true
		}
	}
	if !dropped {
		// Nothing to weaken: callers only use the text when a hypothesis was dropped.
		return "", false
	}
	var out strings.Builder
	out.Grow(len(query))
	for i, command := range commands {
		if i < goalIndex && drop[i] {
			if replacements[i] != "" {
				out.WriteString(replacements[i])
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteString(command)
		out.WriteByte('\n')
	}
	return out.String(), true
}

// A positive quantified Boolean subterm can be replaced by true; beneath not,
// replace it by false instead. And/or preserve this implication in either
// polarity. Unknown operators containing quantifiers lose their whole Boolean
// term, rather than assuming a polarity for (for example) equality or ite.
// Thus the original assertion always implies the emitted assertion. SAT and
// UNKNOWN still require the original query; only UNSAT of this weakening proves.
func smtQuantifierFreeConsequence(command string) (string, bool) {
	// A direct quantified assertion, or a quantified term under an opaque
	// operator, has no scalar consequence this pass can retain. Avoid parsing
	// its body; these direct axioms account for most quantified commands.
	scan := smtAtomScanner{text: command}
	head, _ := scan.next()
	op, _ := scan.next()
	if head != "assert" || (op != "and" && op != "or" && op != "not") {
		return "", false
	}
	if len(command) > 1<<20 {
		return "", false
	}
	var tokens []smtAlphaToken
	if !smtAlphaWalk(command, func(tok smtAlphaToken, _ int) bool { tokens = append(tokens, tok); return true }) {
		return "", false
	}
	if len(tokens) < 4 || tokens[0].text != "(" || tokens[1].text != "assert" {
		return "", false
	}
	ends := make([]int, len(tokens))
	quantifiers := make([]int, len(tokens)+1)
	var stack []int
	for i, tok := range tokens {
		ends[i] = i + 1
		quantifiers[i+1] = quantifiers[i]
		if tok.kind == 'a' && (tok.text == "forall" || tok.text == "exists") {
			quantifiers[i+1]++
		}
		if tok.kind == 'p' {
			switch tok.text {
			case "(":
				stack = append(stack, i)
			case ")":
				if len(stack) == 0 {
					return "", false
				}
				open := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				ends[open] = i + 1
			}
		}
	}
	if len(stack) != 0 || ends[0] != len(tokens) || ends[2] != len(tokens)-1 {
		return "", false
	}
	var out strings.Builder
	out.Grow(min(len(command), 1024))
	out.WriteString("(assert ")
	var emit func(int, bool, int)
	emit = func(start int, positive bool, depth int) {
		end := ends[start]
		if quantifiers[end] == quantifiers[start] {
			for i := start; i < end; i++ {
				if i > start && tokens[i-1].text != "(" && tokens[i].text != ")" {
					out.WriteByte(' ')
				}
				out.WriteString(tokens[i].text)
			}
			return
		}
		if depth < 256 && tokens[start].text == "(" && start+2 < end {
			head := tokens[start+1].text
			if head == "and" || head == "or" {
				out.WriteByte('(')
				out.WriteString(head)
				for child := start + 2; child < end-1; child = ends[child] {
					out.WriteByte(' ')
					emit(child, positive, depth+1)
				}
				out.WriteByte(')')
				return
			}
			if head == "not" && ends[start+2] == end-1 {
				out.WriteString("(not ")
				emit(start+2, !positive, depth+1)
				out.WriteByte(')')
				return
			}
		}
		if positive {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	}
	emit(2, true, 0)
	out.WriteByte(')')
	return out.String(), true
}
