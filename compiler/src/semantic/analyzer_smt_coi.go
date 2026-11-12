package semantic

import "elisacore/src/smtquery"

// Query analysis lives in smtquery so it can be built and maintained separately
// from the analyzer. These adapters preserve the analyzer's internal API.
type smtAtomScanner struct {
	text   string
	offset int
}

func (scan *smtAtomScanner) next() (string, bool) {
	atom, offset, ok := smtquery.NextAtom(scan.text, scan.offset)
	scan.offset = offset
	return atom, ok
}

func smtSplitTopLevel(text string) ([]string, bool) { return smtquery.SplitTopLevel(text) }
func smtScanTopLevel(text string, out []string, limit int) ([]string, bool) {
	return smtquery.ScanTopLevel(text, out, limit)
}
func smtCommandHead(text string) string { return smtquery.CommandHead(text) }
func smtSliceQuery(query, goal string) (string, string, bool) {
	return smtquery.SliceQuery(query, goal)
}
func smtSliceQueryCommands(commands []string, goal string, quantified bool) (string, string, bool) {
	return smtquery.SliceQueryCommands(commands, goal, quantified)
}
