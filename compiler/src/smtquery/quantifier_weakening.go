package smtquery

import (
	"elisacore/src/smtalpha"
	"strings"
)

// WithoutQuantifiedHypotheses retains quantifier-free consequences before a
// quantifier-free goal. This weakens the query: UNSAT still proves the original,
// but SAT or UNKNOWN says nothing and must fall back to the full hypotheses.
// Declarations, definitions, the goal and all subsequent commands stay intact.
func WithoutQuantifiedHypotheses(query, goalAssert string) (string, bool) {
	return WithoutQuantifiedHypothesesCommands(query, goalAssert, nil)
}

// commands may be supplied by the caller when the same query will also be sliced.
func WithoutQuantifiedHypothesesCommands(query, goalAssert string, commands []string) (string, bool) {
	if !strings.Contains(query, "forall") && !strings.Contains(query, "exists") {
		return "", false
	}
	containsQuantifier := func(command string) bool {
		// Neither quantifier atom can occur without its spelling. Most commands
		// are quantifier-free, so avoid tokenizing them at all.
		if !strings.Contains(command, "forall") && !strings.Contains(command, "exists") {
			return false
		}
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
	if commands == nil {
		var ok bool
		commands, ok = SplitTopLevel(query)
		if !ok {
			return "", false
		}
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
	var out strings.Builder
	dropped := false
	for i, command := range commands {
		if i < goalIndex && CommandHead(command) == "assert" && containsQuantifier(command) {
			if !dropped {
				// The prefix is unchanged. Copy it only when a weakening exists.
				out.Grow(len(query))
				for _, kept := range commands[:i] {
					out.WriteString(kept)
					out.WriteByte('\n')
				}
				dropped = true
			}
			if consequence, ok := QuantifierFreeConsequence(command); ok {
				out.WriteString(consequence)
				out.WriteByte('\n')
			}
			continue
		}
		if dropped {
			out.WriteString(command)
			out.WriteByte('\n')
		}
	}
	if !dropped {
		return "", false
	}
	return out.String(), true
}

// A positive quantified Boolean subterm can be replaced by true; beneath not,
// replace it by false instead. And/or preserve this implication in either
// polarity. Unknown operators containing quantifiers lose their whole Boolean
// term, rather than assuming a polarity for (for example) equality or ite.
// Thus the original assertion always implies the emitted assertion. SAT and
// UNKNOWN still require the original query; only UNSAT of this weakening proves.
func QuantifierFreeConsequence(command string) (string, bool) {
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
	var tokens []smtalpha.Token
	if !smtalpha.Walk(command, func(tok smtalpha.Token, _ int) bool { tokens = append(tokens, tok); return true }) {
		return "", false
	}
	if len(tokens) < 4 || tokens[0].Text != "(" || tokens[1].Text != "assert" {
		return "", false
	}
	ends := make([]int, len(tokens))
	quantifiers := make([]int, len(tokens)+1)
	var stack []int
	for i, tok := range tokens {
		ends[i] = i + 1
		quantifiers[i+1] = quantifiers[i]
		if tok.Kind == 'a' && (tok.Text == "forall" || tok.Text == "exists") {
			quantifiers[i+1]++
		}
		if tok.Kind == 'p' {
			switch tok.Text {
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
				if i > start && tokens[i-1].Text != "(" && tokens[i].Text != ")" {
					out.WriteByte(' ')
				}
				out.WriteString(tokens[i].Text)
			}
			return
		}
		if depth < 256 && tokens[start].Text == "(" && start+2 < end {
			head := tokens[start+1].Text
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
