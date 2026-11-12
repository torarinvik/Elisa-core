package semantic

import "elisacore/src/smtquery"

func smtWithoutQuantifiedHypotheses(query, goal string) (string, bool) {
	return smtquery.WithoutQuantifiedHypotheses(query, goal)
}
func smtQuantifierFreeConsequence(command string) (string, bool) {
	return smtquery.QuantifierFreeConsequence(command)
}
