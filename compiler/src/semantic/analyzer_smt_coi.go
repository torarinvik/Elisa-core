package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"elisacore/src/smt"
)

// Cone-of-influence slicing of a solver query.
//
// A query is `declarations + hypotheses + (assert (not goal)) + readback definitions`. Split the
// hypotheses by the DECLARED symbols they mention (constants, functions, defined functions; array
// and quantified facts included, through every declared symbol in them), transitively: the goal's
// component is every hypothesis connected to the goal through a chain of shared symbols. The rest
// mentions no symbol of that component, so the query is the conjunction of two symbol-disjoint
// parts:
//
//	full = sliced ∧ dropped
//
// and therefore: sliced unsat ⇒ full unsat (proven); dropped unsat ⇒ full unsat (a dead path,
// proven); sliced sat ∧ dropped sat ⇒ full sat (the two models combine). No verdict can flip from
// "not proven" to "proven" except through one of the two unsat cases, each of which implies the
// full query is unsat — the slice never proves a goal the full query would not.
//
// Anything the symbol analysis cannot see declines slicing: sort declarations (a quantifier over
// an uninterpreted sort constrains every value of that sort without naming it), unbalanced text,
// or a goal that mentions no declared symbol. Ground hypotheses (no declared symbol) stay in the
// sliced query: they cannot connect anything but may be contradictory on their own.

type smtTopLevelCommand struct {
	text string
	head string // `assert`, `declare-const`, …
	name string // the declared/defined name for declarations
	syms []string
}

// smtSplitTopLevel splits SMT-LIB text into top-level s-expressions (comments dropped). It
// returns false on unbalanced text.
func smtSplitTopLevel(text string) ([]string, bool) {
	var out []string
	depth := 0
	start := -1
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch c {
		case '|':
			j := strings.IndexByte(text[i+1:], '|')
			if j < 0 {
				return nil, false
			}
			i += j + 1
		case '"':
			j := i + 1
			for j < len(text) {
				if text[j] == '"' {
					if j+1 < len(text) && text[j+1] == '"' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(text) {
				return nil, false
			}
			i = j
		case ';':
			if depth == 0 || start >= 0 {
				for i < len(text) && text[i] != '\n' {
					i++
				}
			}
		case '(':
			if depth == 0 {
				start = i
			}
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, false
			}
			if depth == 0 {
				out = append(out, text[start:i+1])
				start = -1
			}
		}
	}
	return out, depth == 0
}

// smtAtomScanner streams symbol atoms without building a temporary slice.
// Quoted |…| symbols stay whole; string literals are skipped, matching the
// previous atom collector. It owns no storage beyond the current query string.
type smtAtomScanner struct {
	text   string
	offset int
}

// smtAtomDelimiter marks the bytes that end a plain atom: `() \n\t\r|"`.
var smtAtomDelimiter = [256]bool{'(': true, ')': true, ' ': true, '\n': true, '\t': true, '\r': true, '|': true, '"': true}

func (scan *smtAtomScanner) next() (string, bool) {
	sexpr := scan.text
	for scan.offset < len(sexpr) {
		i := scan.offset
		c := sexpr[i]
		switch {
		case c == '(' || c == ')' || c == ' ' || c == '\n' || c == '\t' || c == '\r':
			scan.offset++
		case c == '|':
			j := strings.IndexByte(sexpr[i+1:], '|')
			if j < 0 {
				scan.offset = len(sexpr)
				return "", false
			}
			scan.offset = i + j + 2
			return sexpr[i:scan.offset], true
		case c == '"':
			j := i + 1
			for j < len(sexpr) && sexpr[j] != '"' {
				j++
			}
			scan.offset = j + 1
		default:
			j := i
			for j < len(sexpr) && !smtAtomDelimiter[sexpr[j]] {
				j++
			}
			scan.offset = j
			return sexpr[i:j], true
		}
	}
	return "", false
}

func smtCommandHead(sexpr string) string {
	scan := smtAtomScanner{text: sexpr}
	head, _ := scan.next()
	return head
}

// smtSliceQuery splits a query around its goal assert. sliced holds every command except the
// hypotheses outside the goal's cone; dropped holds the declarations plus those hypotheses. ok is
// false when slicing does not apply (nothing to drop, or a shape the analysis does not model).
func smtSliceQuery(query, goalAssert string) (sliced, dropped string, ok bool) {
	return smtSliceQueryFiltered(query, goalAssert, false)
}

// smtSliceQueryFiltered is smtSliceQuery; with requireQuantifiedDrop it declines before
// assembling any text when the dropped part would contain no `(forall ` / `(exists `.
func smtSliceQueryFiltered(query, goalAssert string, requireQuantifiedDrop bool) (sliced, dropped string, ok bool) {
	exprs, balanced := smtSplitTopLevel(query)
	if !balanced {
		return "", "", false
	}
	goalIndex := -1
	for i := len(exprs) - 1; i >= 0; i-- {
		if exprs[i] == goalAssert {
			goalIndex = i
			break
		}
	}
	if goalIndex < 0 {
		return "", "", false
	}
	cmds := make([]smtTopLevelCommand, len(exprs))
	declared := map[string]bool{}
	for i, e := range exprs {
		scan := smtAtomScanner{text: e}
		head, present := scan.next()
		if !present {
			return "", "", false
		}
		cmds[i] = smtTopLevelCommand{text: e, head: head}
		switch head {
		case "declare-const", "declare-fun", "define-fun", "define-const", "define-fun-rec":
			name, present := scan.next()
			if !present {
				return "", "", false
			}
			cmds[i].name = name
			declared[name] = true
		case "assert", "set-option", "set-info", "set-logic":
		default:
			// declare-sort / define-sort / declare-datatype(s) / push / … : not modeled.
			return "", "", false
		}
	}
	for i := range cmds {
		if cmds[i].head == "assert" || cmds[i].head == "define-fun" || cmds[i].head == "define-const" || cmds[i].head == "define-fun-rec" {
			seen := map[string]bool{}
			scan := smtAtomScanner{text: cmds[i].text}
			_, _ = scan.next() // command head is not a referenced symbol
			for atom, present := scan.next(); present; atom, present = scan.next() {
				if declared[atom] && !seen[atom] {
					seen[atom] = true
					cmds[i].syms = append(cmds[i].syms, atom)
				}
			}
		}
	}
	// Union-find over declared symbols: every assert and every definition (name + body) joins
	// the symbols it mentions.
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if p, ok := parent[x]; ok && p != x {
			r := find(p)
			parent[x] = r
			return r
		}
		parent[x] = x
		return x
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			if ra < rb {
				parent[rb] = ra
			} else {
				parent[ra] = rb
			}
		}
	}
	for i := range cmds {
		group := cmds[i].syms
		if cmds[i].name != "" && cmds[i].head != "declare-const" && cmds[i].head != "declare-fun" {
			group = append([]string{cmds[i].name}, group...)
		}
		for j := 1; j < len(group); j++ {
			union(group[0], group[j])
		}
	}
	goalSyms := cmds[goalIndex].syms
	if len(goalSyms) == 0 {
		return "", "", false
	}
	goalRoots := map[string]bool{}
	for _, s := range goalSyms {
		goalRoots[find(s)] = true
	}
	if requireQuantifiedDrop {
		quantified := false
		for i, c := range cmds {
			hypothesis := c.head == "assert" && i < goalIndex
			dropped := hypothesis && len(c.syms) > 0 && !goalRoots[find(c.syms[0])]
			if (dropped || c.head != "assert") && (strings.Contains(c.text, "(forall ") || strings.Contains(c.text, "(exists ")) {
				quantified = true
				break
			}
		}
		if !quantified {
			return "", "", false
		}
	}
	var keep, drop strings.Builder
	droppedAny := false
	for i, c := range cmds {
		hypothesis := c.head == "assert" && i < goalIndex
		if hypothesis && len(c.syms) > 0 && !goalRoots[find(c.syms[0])] {
			drop.WriteString(c.text)
			drop.WriteByte('\n')
			droppedAny = true
			continue
		}
		keep.WriteString(c.text)
		keep.WriteByte('\n')
		if c.head != "assert" {
			drop.WriteString(c.text)
			drop.WriteByte('\n')
		}
	}
	if !droppedAny {
		return "", "", false
	}
	return keep.String(), drop.String(), true
}

// smtDeadPathBudget bounds the side check that the hypotheses outside the goal's cone are
// contradictory on their own (z3 rlimit units).
const smtDeadPathBudget = 20000

// smtVerdict names the solver result type for analyzer fields declared outside this file.
type smtVerdict = smt.Result

type smtBudgetedSolver interface {
	CheckBudget(assertions string, rlimit int64) smt.Result
	CheckBudgetValues(assertions string, vars []string, rlimit int64) (smt.Result, map[string]string)
}

// smtAfterSatAbstractionBudget bounds the full Int<->BV query once its bit-vector abstraction
// is definitively SAT: the abstraction already has a model, so only real bit semantics beyond
// the AND bound lemma could still prove the goal. A lower budget only forgoes such proofs.
const smtAfterSatAbstractionBudget = 50000

// smtCheckTransformedProof reuses only definitive UNSAT for a complete transformed
// query. SAT models and Unknown results must follow the existing solver path.
// The outer query initializes the cache; nil keeps caching fully disabled.
func (a *Analyzer) smtCheckTransformedProof(query string, check func() (smt.Result, map[string]string)) (smt.Result, map[string]string) {
	if a.smtProofCache.contains(query) {
		a.smtStats.CacheHits++
		a.smtStats.PersistentCacheHits++
		return smt.Unsat, nil
	}
	result, model := check()
	if result == smt.Unsat {
		a.smtProofCache.publish(query)
	}
	return result, model
}

// smtCheckSliced decides a query through its cone of influence: the sliced query first, then —
// only if that does not prove — whether the dropped hypotheses are unsatisfiable by themselves
// (a dead path), under a small budget. See smtSliceQuery for why both unsat cases prove the full
// query and why a sat sliced query plus a sat dropped part is the full query's sat.
func (a *Analyzer) smtCheckSliced(solver smt.Solver, query, goalAssert string, vars []string) (smt.Result, map[string]string) {
	res, model := a.smtCheckSlicedOnce(solver, query, goalAssert, vars)
	if res != smt.Unknown {
		return res, model
	}
	budgeted, ok := solver.(smtBudgetedSolver)
	if !ok {
		return res, model
	}
	// An undecided query whose quantified hypotheses drown a simple arithmetic goal: the
	// quantifier-free weakening ran first under the small dead-path budget; give it the
	// query budget now. Dropping hypotheses only weakens, so only its unsat is used.
	stripped := smtStripAbstractionOnly(query)
	if weaker, ok := smtWithoutQuantifiedHypotheses(stripped, goalAssert); ok {
		a.smtSetLogLabelForQuery(solver, "quantifier-weakening-full", goalAssert, weaker)
		weak, _ := a.smtCheckTransformedProof(weaker+"; full budget\n", func() (smt.Result, map[string]string) {
			return budgeted.CheckBudgetValues(weaker, vars, smtResourceLimit())
		})
		if weak == smt.Unsat {
			return smt.Unsat, nil
		}
	}
	if a.smtEscalation == 0 {
		return res, model
	}
	// withSMTEscalation: retry the complete query under the larger budget.
	a.smtSetLogLabelForQuery(solver, "escalated", goalAssert, stripped)
	res, model = a.smtCheckTransformedProof(stripped+"; escalated\n", func() (smt.Result, map[string]string) {
		return budgeted.CheckBudgetValues(stripped, vars, smtResourceLimit()*smtEscalationFactor)
	})
	if res != smt.Unknown {
		return res, model
	}
	// Retry the complete query using an alternate Z3 search strategy before
	// replaying history. Definitive results apply to the same assertions.
	if alternate, ok := solver.(interface {
		CheckAlternateRelevancy(string, []string, int64) (smt.Result, map[string]string, bool)
	}); ok {
		a.smtSetLogLabelForQuery(solver, "escalated-relevancy", goalAssert, stripped)
		r, m, supported := alternate.CheckAlternateRelevancy(stripped, vars, smtResourceLimit()*smtEscalationFactor)
		if supported && r != smt.Unknown {
			return r, m
		}
	}
	// Some proofs need lemmas a session learns from the body's earlier obligations. Replay them
	// in one session, then ask again. The history is this body's own asking order, so the
	// verdict is reproducible; an unsat is cached like any other proof.
	warm, ok := solver.(smtHistorySolver)
	history := a.smtHistoryBefore(query)
	if !ok || len(history) == 0 {
		return res, model
	}
	a.smtSetLogLabelForQuery(solver, "escalated-history", goalAssert, stripped)
	return a.smtCheckTransformedProof(stripped+"; escalated after history "+smtHistoryDigest(history)+"\n", func() (smt.Result, map[string]string) {
		return warm.CheckAfterHistory(history, stripped, vars, smtResourceLimit()*smtEscalationFactor)
	})
}

type smtHistorySolver interface {
	CheckAfterHistory(history []string, assertions string, vars []string, rlimit int64) (smt.Result, map[string]string)
}

// smtHistoryBefore is the body's history up to (excluding) the query's latest entry.
func (a *Analyzer) smtHistoryBefore(query string) []string {
	if a.smtHistoryFor != a.currentFuncDecl {
		return nil
	}
	history := a.smtHistory
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] == query {
			return history[:i]
		}
	}
	return history
}

func smtHistoryDigest(history []string) string {
	hash := sha256.New()
	for _, entry := range history {
		hash.Write([]byte(entry))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (a *Analyzer) smtCheckSlicedOnce(solver smt.Solver, query, goalAssert string, vars []string) (smt.Result, map[string]string) {
	// Bit-vector abstraction first (analyzer_smt_bv_abstract.go): an unsat weakening proves.
	budgeted, canBudget := solver.(smtBudgetedSolver)
	check := func(q string) (smt.Result, map[string]string) {
		res, model, _ := solver.CheckValues(q, vars)
		return res, model
	}
	if abstracted, ok := smtAbstractBitvectorReads(query); ok {
		a.smtSetLogLabelForQuery(solver, "bv-abstract", goalAssert, abstracted)
		res, _ := a.smtCheckTransformedProof(abstracted, func() (smt.Result, map[string]string) {
			result, model, _ := solver.CheckValues(abstracted, nil)
			return result, model
		})
		if res == smt.Unsat {
			return smt.Unsat, nil
		}
		a.smtSetLogLabelForQuery(solver, "query", goalAssert, query)
		if res == smt.Sat && canBudget && smtOnlyAndBitvectorOps(query) {
			check = func(q string) (smt.Result, map[string]string) {
				return budgeted.CheckBudgetValues(q, vars, smtAfterSatAbstractionBudget)
			}
		}
	}
	query = smtStripAbstractionOnly(query)
	// Weakening and slicing only act on quantified hypotheses. Text without a quantifier
	// spelling has no quantifier atom, so both would decline after re-parsing every command.
	if !strings.Contains(query, "forall") && !strings.Contains(query, "exists") {
		return check(query)
	}
	// Related quantified callee clauses can overwhelm an otherwise simple
	// arithmetic proof. Try a bounded weakening first, accepting UNSAT only.
	if canBudget {
		if weaker, ok := smtWithoutQuantifiedHypotheses(query, goalAssert); ok {
			a.smtSetLogLabelForQuery(solver, "quantifier-weakening", goalAssert, weaker)
			budget := int64(smtDeadPathBudget)
			if a.smtEscalation > 0 {
				// A reported obligation: its cheap weakening also gets the larger budget.
				budget *= smtEscalationFactor
			}
			res, model := a.smtCheckTransformedProof(weaker, func() (smt.Result, map[string]string) {
				return budgeted.CheckBudgetValues(weaker, vars, budget)
			})
			if res == smt.Unsat {
				return res, model
			}
			a.smtSetLogLabelForQuery(solver, "query", goalAssert, query)
		}
	}
	sliced, dropped, ok := smtSliceQueryFiltered(query, goalAssert, true)
	// Slice only when the dropped part carries quantifiers: quantifier instantiation is what burns
	// the budget on irrelevant facts. A quantifier-free remainder is cheap inside the full query,
	// and slicing it would only add a second solver round trip.
	if ok && !strings.Contains(dropped, "(forall ") && !strings.Contains(dropped, "(exists ") {
		ok = false
	}
	if !ok || !canBudget {
		return check(query)
	}
	res, model := a.smtCheckTransformedProof(sliced, func() (smt.Result, map[string]string) { return check(sliced) })
	if res == smt.Unsat {
		return res, model
	}
	// The same out-of-cone hypotheses recur across a function's obligations: memoize the
	// definitive dead-path verdicts (unknown is retried, as in the query cache).
	if a.smtDeadPathCache == nil {
		a.smtDeadPathCache = map[string]smt.Result{}
	}
	deadPath, cached := a.smtDeadPathCache[dropped]
	if !cached {
		a.smtSetLogLabelForQuery(solver, "deadpath", goalAssert, dropped)
		deadPath, _ = a.smtCheckTransformedProof(dropped, func() (smt.Result, map[string]string) {
			return budgeted.CheckBudget(dropped, smtDeadPathBudget), nil
		})
		if deadPath != smt.Unknown {
			a.smtDeadPathCache[dropped] = deadPath
		}
	}
	if deadPath == smt.Unsat {
		return smt.Unsat, nil
	}
	if res == smt.Sat && deadPath != smt.Sat {
		// The full query is sat only if the dropped part is too; undecided ⇒ undecided (no model).
		return smt.Unknown, nil
	}
	return res, model
}
