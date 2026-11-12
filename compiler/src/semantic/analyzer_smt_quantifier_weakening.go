package semantic

import "elisacore/src/smtquery"

func smtWithoutQuantifiedHypothesesCommands(query, goal string, commands []string) (string, bool) {
	return smtquery.WithoutQuantifiedHypothesesCommands(query, goal, commands)
}
