package semantic

import (
	"fmt"
	"os"
	"reflect"
	"sort"

	"elisacore/src/ast"
)

// docs/91 G0 — read-only death-point + cohort analysis.
//
// This is the FIRST, NON-DESTRUCTIVE step of the global death-time region model (docs/91): it
// computes, per function, where each INFERRED heap allocation (an un-annotated darray/dict/struct
// the compiler auto-regions) is last used — its approximate "death point" — and groups allocations
// that die together into cohorts. A cohort is the region the death-time model WOULD form. The point
// is observability: we can inspect, on real programs, the cohorts and free-points the model implies
// BEFORE any allocation is freed earlier (G1). It changes no codegen and runs only under the
// ELISA_DUMP_DEATHTIME env flag.
//
// Soundness note: the death point here is the LEXICAL last-use (last statement that mentions the
// binding), which over-approximates real liveness for straight-line code and is intentionally crude
// — it is a validation aid, not the analysis G1 will free on. A binding that escapes (appears in a
// `return`) is marked death=∞ (its lifetime belongs to the caller). Real interprocedural liveness is
// G3; precise intra-function liveness is the G1 prerequisite.

var dumpDeathTime = os.Getenv("ELISA_DUMP_DEATHTIME") != ""

type deathTimeAlloc struct {
	name       string
	declIndex  int
	deathIndex int // statement index of last use; -1 == escapes (death deferred to caller)
	kind       string
}

// DeathTimeCohort is one inferred death cohort (docs/91 G0): the set of inferred allocations that
// share a death point — the region the global death-time model would form. Exported for inspection
// and tests. DeathIndex is the statement index they die at, or -1 if the cohort escapes to the
// caller. Growables is how many members are own-stack growables (the rest share the cohort's fixed
// stack, per docs/71).
type DeathTimeCohort struct {
	DeathIndex int
	Allocs     []string
	Growables  int
}

// childStmtBlocks returns the nested statement bodies of a statement (mirrors the block set the
// region-lifetime walks already recurse into), so the function body can be indexed in pre-order.
func childStmtBlocks(stmt ast.Stmt) [][]ast.Stmt {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		out := [][]ast.Stmt{s.Then}
		for _, e := range s.Elifs {
			out = append(out, e.Body)
		}
		out = append(out, s.Else)
		return out
	case *ast.WhileStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.ForStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.IterForStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.ScopeStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.CanStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.RegionStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.InStoreStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.MatchStmt:
		out := make([][]ast.Stmt, 0, len(s.Arms))
		for _, arm := range s.Arms {
			out = append(out, arm.Body)
		}
		return out
	}
	return nil
}

// stmtMentionsName reports whether a statement's subtree references an identifier with the given
// name. Walks via reflection (the same approach the region growth scan uses) so it covers every
// expression position without enumerating node kinds. It descends into nested statement blocks too;
// that only attributes a nested use to the enclosing statement as well, which is harmless because
// the caller takes the MAX mentioning index (the nested statement, at a higher pre-order index,
// dominates).
func stmtMentionsName(stmt ast.Stmt, name string) bool {
	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil {
				if id.Name == name {
					found = true
				}
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if !v.Field(i).CanInterface() {
					continue
				}
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(stmt))
	return found
}

// analyzeDeathTimeAllocs computes the inferred allocations of a function and their death points.
func (a *Analyzer) analyzeDeathTimeAllocs(fn *ast.FuncDecl) []deathTimeAlloc {
	var stmts []ast.Stmt
	var collect func([]ast.Stmt)
	collect = func(body []ast.Stmt) {
		for _, s := range body {
			stmts = append(stmts, s)
			for _, sub := range childStmtBlocks(s) {
				collect(sub)
			}
		}
	}
	collect(fn.Body)

	var allocs []deathTimeAlloc
	for i, s := range stmts {
		vd, ok := s.(*ast.VarDeclStmt)
		if !ok {
			continue
		}
		kind := ""
		switch {
		case a.isFreshRegionAllocation(vd):
			kind = "growable"
		case isFreshAutoStructAllocation(vd):
			kind = "fixed"
		default:
			continue
		}
		allocs = append(allocs, deathTimeAlloc{name: vd.Name, declIndex: i, deathIndex: i, kind: kind})
	}

	// Identify escaping bindings (appear in a return) and compute lexical last-use otherwise.
	returnsName := func(name string) bool {
		for _, s := range stmts {
			if ret, ok := s.(*ast.ReturnStmt); ok && ret.Value != nil && stmtMentionsName(ret, name) {
				return true
			}
		}
		return false
	}
	for k := range allocs {
		al := &allocs[k]
		if returnsName(al.name) {
			al.deathIndex = -1
			continue
		}
		for i := al.declIndex + 1; i < len(stmts); i++ {
			if stmtMentionsName(stmts[i], al.name) {
				al.deathIndex = i
			}
		}
	}
	return allocs
}

// computeDeathTimeCohorts groups a function's inferred allocations into death cohorts (docs/91 G0).
func (a *Analyzer) computeDeathTimeCohorts(fn *ast.FuncDecl) []DeathTimeCohort {
	if fn == nil || len(fn.Body) == 0 {
		return nil
	}
	allocs := a.analyzeDeathTimeAllocs(fn)
	if len(allocs) == 0 {
		return nil
	}
	byDeath := map[int][]deathTimeAlloc{}
	for _, al := range allocs {
		byDeath[al.deathIndex] = append(byDeath[al.deathIndex], al)
	}
	deaths := make([]int, 0, len(byDeath))
	for d := range byDeath {
		deaths = append(deaths, d)
	}
	sort.Ints(deaths) // -1 (escapes) sorts first
	cohorts := make([]DeathTimeCohort, 0, len(deaths))
	for _, d := range deaths {
		members := byDeath[d]
		sort.Slice(members, func(i, j int) bool { return members[i].name < members[j].name })
		c := DeathTimeCohort{DeathIndex: d}
		for _, al := range members {
			c.Allocs = append(c.Allocs, al.name)
			if al.kind == "growable" {
				c.Growables++
			}
		}
		cohorts = append(cohorts, c)
	}
	return cohorts
}

// recordDeathTimeCohorts computes a function's cohorts, stores them on the analyzer (surfaced on
// Result for inspection/tests), and dumps them under ELISA_DUMP_DEATHTIME (docs/91 G0). Read-only:
// no codegen impact. Only invoked when the dump flag is set, so normal builds pay nothing.
func (a *Analyzer) recordDeathTimeCohorts(fn *ast.FuncDecl) {
	cohorts := a.computeDeathTimeCohorts(fn)
	if len(cohorts) == 0 {
		return
	}
	if a.deathTimeCohorts == nil {
		a.deathTimeCohorts = map[string][]DeathTimeCohort{}
	}
	a.deathTimeCohorts[fn.Name] = cohorts
	total := 0
	for _, c := range cohorts {
		total += len(c.Allocs)
	}
	fmt.Fprintf(os.Stderr, "death-time cohorts for %s: %d cohort(s) over %d inferred allocation(s)\n", fn.Name, len(cohorts), total)
	for _, c := range cohorts {
		label := fmt.Sprintf("dies@stmt%d", c.DeathIndex)
		if c.DeathIndex == -1 {
			label = "escapes (caller region)"
		}
		fmt.Fprintf(os.Stderr, "  cohort %s: %v  [%d growable -> %d own-stack(s) + %d shared]\n",
			label, c.Allocs, c.Growables, c.Growables, len(c.Allocs)-c.Growables)
	}
}
