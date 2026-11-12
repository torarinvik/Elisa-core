package semantic

import "elisacore/src/smtquery"

func smtDeduplicateAssertions(query, goal string) string {
	return smtquery.DeduplicateAssertions(query, goal)
}
