package semantic

import "strings"

// smtDeduplicateAssertions removes identical, unlabelled top-level assertions
// from generated line-oriented queries. It leaves the goal in place for the
// cone-of-influence and weakening passes. Any unfamiliar or multiline command
// declines the optimization, preserving the original query and its errors.
func smtDeduplicateAssertions(query, goal string) string {
	if len(query) < 4096 {
		return query
	}
	lines := strings.SplitAfter(query, "\n")
	seen := make(map[string]struct{})
	var duplicate []int
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		// A previously validated identical assertion needs no second parse.
		// Unsafe commands later in the query still cancel the entire rewrite.
		if _, exists := seen[text]; exists {
			duplicate = append(duplicate, i)
			continue
		}
		commands, ok := smtSplitTopLevel(text)
		if !ok || len(commands) != 1 || commands[0] != text {
			return query
		}
		switch smtCommandHead(text) {
		case "declare-const", "declare-fun", "define-fun", "define-const", "define-fun-rec":
		case "assert":
			// Named assertions are observable to SMT clients; do not alter them.
			if text == goal || strings.Contains(text, ":named") {
				continue
			}
			seen[text] = struct{}{}
		default:
			return query
		}
	}
	if len(duplicate) == 0 {
		return query
	}
	var compact strings.Builder
	compact.Grow(len(query))
	d := 0
	for i, line := range lines {
		if d < len(duplicate) && duplicate[d] == i {
			d++
			continue
		}
		compact.WriteString(line)
	}
	return compact.String()
}
