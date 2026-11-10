package semantic

import (
	"strings"
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
	// Generated SMT commands generally occupy one line. Reserve a bounded
	// estimate so long preambles do not repeatedly grow the output slice.
	var out []string
	if lines := strings.Count(text, "\n"); lines > 1 {
		out = make([]string, 0, min(lines, 4096))
	}
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
	if len(out) == 0 {
		return nil, depth == 0
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
	return smtSliceQueryCommands(exprs, goalAssert, requireQuantifiedDrop)
}

func smtSliceQueryCommands(exprs []string, goalAssert string, requireQuantifiedDrop bool) (sliced, dropped string, ok bool) {
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
			// Most commands mention only a few declared symbols. Scan the
			// existing small slice and allocate a set only for wider commands.
			var seen map[string]bool
			scan := smtAtomScanner{text: cmds[i].text}
			_, _ = scan.next() // command head is not a referenced symbol
			for atom, present := scan.next(); present; atom, present = scan.next() {
				if !declared[atom] {
					continue
				}
				duplicate := false
				if seen != nil {
					duplicate = seen[atom]
				} else {
					for _, prior := range cmds[i].syms {
						if prior == atom {
							duplicate = true
							break
						}
					}
				}
				if duplicate {
					continue
				}
				cmds[i].syms = append(cmds[i].syms, atom)
				if seen != nil {
					seen[atom] = true
				} else if len(cmds[i].syms) == 8 {
					seen = make(map[string]bool, 16)
					for _, symbol := range cmds[i].syms {
						seen[symbol] = true
					}
				}
			}
		}
	}
	// Union-find over declared symbols: every assert and every definition (name + body) joins
	// the symbols it mentions.
	// Resolve symbol names once; component traversal uses compact integer IDs.
	ids := make(map[string]int, len(declared))
	parents := make([]int, len(declared))
	sizes := make([]int, len(declared))
	for symbol := range declared {
		id := len(ids)
		ids[symbol] = id
		parents[id], sizes[id] = id, 1
	}
	find := func(symbol string) int {
		id := ids[symbol]
		for parents[id] != id {
			parents[id] = parents[parents[id]]
			id = parents[id]
		}
		return id
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if sizes[ra] < sizes[rb] {
			ra, rb = rb, ra
		}
		parents[rb] = ra
		sizes[ra] += sizes[rb]
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
	goalRoots := make(map[int]bool, len(goalSyms))
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
