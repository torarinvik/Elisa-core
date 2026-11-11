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
	seen := make(map[string]struct{})
	var compact strings.Builder
	var single [1]string
	start, kept := 0, 0
	for line := range strings.Lines(query) {
		end := start + len(line)
		text := strings.TrimSpace(line)
		lineStart := start
		start = end
		if text == "" {
			continue
		}
		// A previously validated identical assertion needs no second parse.
		// Unsafe commands later in the query still cancel the entire rewrite.
		if _, exists := seen[text]; exists {
			if kept == 0 {
				compact.Grow(len(query) - len(line))
			}
			compact.WriteString(query[kept:lineStart])
			kept = end
			continue
		}
		commands, ok := smtScanTopLevel(text, single[:0], 1)
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
	if kept == 0 {
		return query
	}
	compact.WriteString(query[kept:])
	return compact.String()
}
