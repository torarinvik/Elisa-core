package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/smt"
)

type smtLabeledSolver interface {
	LogsRlimit() bool
	SetLogLabel(string)
}

// smtSetLogLabel attributes the next solver checks in the ELISA_SMT_RLIMIT_LOG calibration log:
// `file:line | function | kind | obligation` (obligation truncated). No-op unless logging is on;
// it never affects a verdict.
func (a *Analyzer) smtSetLogLabel(solver smt.Solver, kind, obligation string) {
	a.smtSetLogLabelForQuery(solver, kind, obligation, "")
}

// smtQueryFeatures tags the theories a query uses (attribution only): bv = int2bv/bv2nat
// bridges, q = quantifiers, arr = array reads/writes, nl = a `*`, `div` or `mod` term.
func smtQueryFeatures(query string) string {
	var tags []string
	if strings.Contains(query, "int2bv") || strings.Contains(query, "bv2nat") {
		tags = append(tags, "bv")
	}
	if strings.Contains(query, "(forall ") || strings.Contains(query, "(exists ") {
		tags = append(tags, "q")
	}
	if strings.Contains(query, "(select ") || strings.Contains(query, "(store ") {
		tags = append(tags, "arr")
	}
	if strings.Contains(query, "(* ") || strings.Contains(query, "(div ") {
		tags = append(tags, "nl")
	}
	if len(tags) == 0 {
		return "lia"
	}
	return strings.Join(tags, ",")
}

// smtSetLogLabelForQuery is smtSetLogLabel with the query's feature tags (when query != "").
func (a *Analyzer) smtSetLogLabelForQuery(solver smt.Solver, kind, obligation, query string) {
	labeled, ok := solver.(smtLabeledSolver)
	if !ok || !labeled.LogsRlimit() {
		return
	}
	if query != "" {
		kind += " [" + smtQueryFeatures(query) + "]"
	}
	where := "?"
	if a.currentFuncDecl != nil {
		where = a.currentFuncDecl.Position.File + ":" + strconv.Itoa(a.currentFuncDecl.Position.Line)
	}
	if len(obligation) > 400 {
		obligation = obligation[:400] + "..."
	}
	labeled.SetLogLabel(where + " | " + a.currentFuncName() + " | " + kind + " | " + obligation)
}
