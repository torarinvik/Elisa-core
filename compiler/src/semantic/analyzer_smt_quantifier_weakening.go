package semantic

import "elisacore/src/smtquery"

func smtWithoutQuantifiedHypotheses(query, goal string) (string, bool) {
	return smtquery.WithoutQuantifiedHypotheses(query, goal)
}
func smtWithoutQuantifiedHypothesesCommands(query, goal string, commands []string) (string, bool) {
	return smtquery.WithoutQuantifiedHypothesesCommands(query, goal, commands)
}
func smtQuantifierFreeConsequence(command string) (string, bool) {
	return smtquery.QuantifierFreeConsequence(command)
}
