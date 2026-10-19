package semantic

import "strings"

// smtWithoutQuantifiedHypotheses removes whole quantified assertions before a
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
	drop := make([]bool, goalIndex)
	dropped := false
	for i := 0; i < goalIndex; i++ {
		if smtCommandHead(commands[i]) == "assert" && containsQuantifier(commands[i]) {
			drop[i] = true
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
			continue
		}
		out.WriteString(command)
		out.WriteByte('\n')
	}
	return out.String(), true
}
