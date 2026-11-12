package semantic

import "elisacore/src/smtquery"

const smtBVAbstractPrefix = smtquery.BVAbstractPrefix
const smtAbstractionOnlyMarker = smtquery.AbstractionOnlyMarker

func smtOnlyAndBitvectorOps(query string) bool    { return smtquery.OnlyAndBitvectorOps(query) }
func smtStripAbstractionOnly(query string) string { return smtquery.StripAbstractionOnly(query) }
func smtAbstractBitvectorReads(query string) (string, bool) {
	return smtquery.AbstractBitvectorReads(query)
}
func smtBitvectorReadWidth(term string) (int, bool) { return smtquery.BitvectorReadWidth(term) }
