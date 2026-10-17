package semantic

import (
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

// smtAtoms returns the symbol atoms of an s-expression (quoted |…| symbols kept whole).
func smtAtoms(sexpr string) []string {
	var atoms []string
	i := 0
	for i < len(sexpr) {
		c := sexpr[i]
		switch {
		case c == '(' || c == ')' || c == ' ' || c == '\n' || c == '\t' || c == '\r':
			i++
		case c == '|':
			j := strings.IndexByte(sexpr[i+1:], '|')
			if j < 0 {
				return atoms
			}
			atoms = append(atoms, sexpr[i:i+j+2])
			i += j + 2
		case c == '"':
			j := i + 1
			for j < len(sexpr) && sexpr[j] != '"' {
				j++
			}
			i = j + 1
		default:
			j := i
			for j < len(sexpr) && !strings.ContainsRune("() \n\t\r|\"", rune(sexpr[j])) {
				j++
			}
			atoms = append(atoms, sexpr[i:j])
			i = j
		}
	}
	return atoms
}

func smtCommandHead(sexpr string) string {
	atoms := smtAtoms(sexpr)
	if len(atoms) == 0 {
		return ""
	}
	return atoms[0]
}

// smtSliceQuery splits a query around its goal assert. sliced holds every command except the
// hypotheses outside the goal's cone; dropped holds the declarations plus those hypotheses. ok is
// false when slicing does not apply (nothing to drop, or a shape the analysis does not model).
func smtSliceQuery(query, goalAssert string) (sliced, dropped string, ok bool) {
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
		atoms := smtAtoms(e)
		if len(atoms) == 0 {
			return "", "", false
		}
		cmds[i] = smtTopLevelCommand{text: e, head: atoms[0]}
		switch atoms[0] {
		case "declare-const", "declare-fun", "define-fun", "define-const", "define-fun-rec":
			if len(atoms) < 2 {
				return "", "", false
			}
			cmds[i].name = atoms[1]
			declared[atoms[1]] = true
		case "assert", "set-option", "set-info", "set-logic":
		default:
			// declare-sort / define-sort / declare-datatype(s) / push / … : not modeled.
			return "", "", false
		}
	}
	for i := range cmds {
		if cmds[i].head == "assert" || cmds[i].head == "define-fun" || cmds[i].head == "define-const" || cmds[i].head == "define-fun-rec" {
			seen := map[string]bool{}
			for _, atom := range smtAtoms(cmds[i].text)[1:] {
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

// smtCheckSliced decides a query through its cone of influence: the sliced query first, then —
// only if that does not prove — whether the dropped hypotheses are unsatisfiable by themselves
// (a dead path), under a small budget. See smtSliceQuery for why both unsat cases prove the full
// query and why a sat sliced query plus a sat dropped part is the full query's sat.
func (a *Analyzer) smtCheckSliced(solver smt.Solver, query, goalAssert string, vars []string) (smt.Result, map[string]string) {
	// Bit-vector abstraction first (analyzer_smt_bv_abstract.go): an unsat weakening proves.
	budgeted, canBudget := solver.(smtBudgetedSolver)
	check := func(q string) (smt.Result, map[string]string) {
		res, model, _ := solver.CheckValues(q, vars)
		return res, model
	}
	if abstracted, ok := smtAbstractBitvectorReads(query); ok {
		a.smtSetLogLabelForQuery(solver, "bv-abstract", goalAssert, abstracted)
		res, _, _ := solver.CheckValues(abstracted, nil)
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
	// Related quantified callee clauses can overwhelm an otherwise simple
	// arithmetic proof. Try a bounded weakening first, accepting UNSAT only.
	if canBudget {
		if weaker, ok := smtWithoutQuantifiedHypotheses(query, goalAssert); ok {
			a.smtSetLogLabelForQuery(solver, "quantifier-weakening", goalAssert, weaker)
			res, model := budgeted.CheckBudgetValues(weaker, vars, smtDeadPathBudget)
			if res == smt.Unsat {
				return res, model
			}
			a.smtSetLogLabelForQuery(solver, "query", goalAssert, query)
		}
	}
	sliced, dropped, ok := smtSliceQuery(query, goalAssert)
	// Slice only when the dropped part carries quantifiers: quantifier instantiation is what burns
	// the budget on irrelevant facts. A quantifier-free remainder is cheap inside the full query,
	// and slicing it would only add a second solver round trip.
	if ok && !strings.Contains(dropped, "(forall ") && !strings.Contains(dropped, "(exists ") {
		ok = false
	}
	if !ok || !canBudget {
		return check(query)
	}
	res, model := check(sliced)
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
		deadPath = budgeted.CheckBudget(dropped, smtDeadPathBudget)
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
