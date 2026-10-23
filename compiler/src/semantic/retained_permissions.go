package semantic

import "elisacore/src/ast"

// Retained permission passes. A full run records, per function, which function
// rows its effect equation reads (permReaders) and which rows its validation
// read along with the diagnostics it produced. A body-only update then
// re-solves effects from the edited functions outward, the same fixpoint the
// dense pass reaches from the retained rows. When no row changed, validation
// re-checks only the edited functions and replays the rest's diagnostics
// (validation reads rows directly too, so any row change re-validates all).
type retainedPermissions struct {
	readers  map[*FuncType]map[int]bool // row -> indices (in permissionFunctionDecls) of its readers
	diags    map[*ast.FuncDecl][]Diagnostic
	changed  map[*FuncType]bool // pre-existing rows changed by the last solve; fresh functions validate as edited bodies
	edited   map[*ast.FuncDecl]bool
	usable   bool // this update has stable equation indexes (body edits or verified append-only growth)
	solvedIn bool // the incremental solve ran in this update
	// rows holds every row as the last solve left it: body analysis can rewrite
	// an edited function's row before the solve runs, so "changed" is judged
	// against this, not against the row the recompute found.
	rows map[*FuncType]permissionRowSnapshot
	// initial holds every row as the solve started from it (the declaration phase's
	// row; for an edited function, the row this update's body analysis left).
	initial map[*FuncType]permissionRowSnapshot
	// declared holds every row as the declaration phase left it, before any body ran: what
	// a body's call to itself (or to a function not yet analyzed) reads in a fresh analysis.
	declared map[*FuncType]permissionRowSnapshot
}

// recordDeclared snapshots the declaration phase's rows, before the full body analysis.
func (p *retainedPermissions) recordDeclared(a *Analyzer, decls []scopedDecl) {
	p.declared = map[*FuncType]permissionRowSnapshot{}
	for _, sd := range permissionFunctionDecls(decls) {
		if ft := permissionDeclType(a, sd); ft != nil {
			p.declared[ft] = snapshotPermissionRow(ft)
		}
	}
}

func (s permissionRowSnapshot) restore(ft *FuncType) {
	ft.PermissionRefs, ft.MutableGlobalPermissionRefs = s.refs, s.mutable
	ft.ReturnedFunctionPermissionRefs, ft.ReturnedFunctionGlobalPermissionRefs = s.returned, s.returnedGlobal
	ft.Permissions = s.families
}

// coveredBy reports whether every entry of s is still in ft's row, judged by name. A ref
// with type arguments or a realization is not compared and counts as possibly lost.
func (s permissionRowSnapshot) coveredBy(ft *FuncType) bool {
	covered := func(old, cur []ast.PermissionRef) bool {
		have := map[[2]string]bool{}
		for _, ref := range cur {
			have[[2]string{ref.Name, ref.Member}] = true
		}
		for _, ref := range old {
			if len(ref.TypeArgs) != 0 || len(ref.Via) != 0 || !have[[2]string{ref.Name, ref.Member}] {
				return false
			}
		}
		return true
	}
	for _, family := range s.families {
		if !containsString(ft.Permissions, family) {
			return false
		}
	}
	return covered(s.refs, ft.PermissionRefs) && covered(s.mutable, ft.MutableGlobalPermissionRefs) &&
		covered(s.returned, ft.ReturnedFunctionPermissionRefs) && covered(s.returnedGlobal, ft.ReturnedFunctionGlobalPermissionRefs)
}

// resetToInitialRows prepares a full solve. Outside an update the current rows are the
// declaration phase's: they are recorded as the initial rows. In an update an edited or
// newly declared function's current row is its initial one; every other row restarts from
// its recorded initial row.
func (p *retainedPermissions) resetToInitialRows(a *Analyzer, decls []scopedDecl, updating bool) {
	if !updating || p.initial == nil {
		p.initial = map[*FuncType]permissionRowSnapshot{}
	}
	for _, sd := range decls {
		ft := permissionDeclType(a, sd)
		if ft == nil {
			continue
		}
		start, known := p.initial[ft]
		if !updating || !known || p.edited[sd.Decl.(*ast.FuncDecl)] {
			p.initial[ft] = snapshotPermissionRow(ft)
			continue
		}
		start.restore(ft)
	}
}

// withInitialRows runs body analysis of the edited functions against the rows a fresh
// analysis's bodies read: every other row's initial one, not its solved one (a caller's
// inferred row takes its callees' rows). The solved rows are put back afterwards for the
// incremental solve; a full solve restarts from the initial rows anyway.
func (p *retainedPermissions) withInitialRows(a *Analyzer, decls []scopedDecl, body func()) {
	if p.initial == nil {
		body()
		return
	}
	editedTypes := map[*FuncType]bool{}
	for fn := range p.edited {
		sym := a.funcDeclSymbols[fn]
		if sym == nil {
			continue
		}
		ft, _ := sym.Type.(*FuncType)
		if ft == nil {
			continue
		}
		editedTypes[ft] = true
		// An edited function keeps its type when its signature did not change, and with
		// it the solved row a recursive call would read; its body starts from its
		// declared row, as in a fresh analysis. The body then writes its own row.
		if start, known := p.declared[ft]; known {
			start.restore(ft)
		} else if p.declared != nil {
			p.declared[ft] = snapshotPermissionRow(ft) // a new type: fresh from this update's declaration phase
		}
	}
	// Every other row reads as its initial one; most already are, so only rows the
	// solve moved are swapped.
	solved := map[*FuncType]permissionRowSnapshot{}
	for ft, start := range p.initial {
		if editedTypes[ft] || start.identical(ft) {
			continue
		}
		solved[ft] = snapshotPermissionRow(ft)
		start.restore(ft)
	}
	body()
	for ft, row := range solved {
		row.restore(ft)
	}
}

// identical reports whether ft's row is the very slices s holds.
func (s permissionRowSnapshot) identical(ft *FuncType) bool {
	same := func(x, y []ast.PermissionRef) bool {
		return len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
	}
	return same(s.refs, ft.PermissionRefs) && same(s.mutable, ft.MutableGlobalPermissionRefs) &&
		same(s.returned, ft.ReturnedFunctionPermissionRefs) && same(s.returnedGlobal, ft.ReturnedFunctionGlobalPermissionRefs) &&
		len(s.families) == len(ft.Permissions) && (len(s.families) == 0 || &s.families[0] == &ft.Permissions[0])
}

type permissionRowSnapshot struct {
	refs, mutable, returned, returnedGlobal []ast.PermissionRef
	families                                []string
}

func snapshotPermissionRow(ft *FuncType) permissionRowSnapshot {
	return permissionRowSnapshot{refs: ft.PermissionRefs, mutable: ft.MutableGlobalPermissionRefs, returned: ft.ReturnedFunctionPermissionRefs, returnedGlobal: ft.ReturnedFunctionGlobalPermissionRefs, families: ft.Permissions}
}

func (s permissionRowSnapshot) same(ft *FuncType) bool {
	return samePermissionRefs(s.refs, ft.PermissionRefs) && samePermissionRefs(s.mutable, ft.MutableGlobalPermissionRefs) &&
		samePermissionRefs(s.returned, ft.ReturnedFunctionPermissionRefs) && samePermissionRefs(s.returnedGlobal, ft.ReturnedFunctionGlobalPermissionRefs) &&
		sameStringSlice(s.families, ft.Permissions)
}

func (p *retainedPermissions) beginUpdate(changed []scopedDecl, usable bool, marks map[string][][2]int) {
	p.edited = map[*ast.FuncDecl]bool{}
	for _, sd := range changed {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			p.edited[fn] = true
		}
	}
	p.usable = usable && p.readers != nil
	p.solvedIn = false
	p.changed = nil
	if len(marks) != 0 {
		for _, diags := range p.diags {
			for i := range diags {
				diags[i].Pos = retainedShiftPos(diags[i].Pos, marks)
			}
		}
	}
}

// recordGraph evaluates every equation once more at the fixpoint, observing
// which rows each reads. Rows do not change (the solve converged).
func (p *retainedPermissions) recordGraph(a *Analyzer, decls []scopedDecl) {
	p.readers = map[*FuncType]map[int]bool{}
	saved := a.permissionInference
	defer func() { a.permissionInference = saved }()
	reader := 0
	a.permissionInference = &permissionInferenceObserver{
		read: func(ft *FuncType) {
			if p.readers[ft] == nil {
				p.readers[ft] = map[int]bool{}
			}
			p.readers[ft][reader] = true
		},
		changed: func(*FuncType) {},
	}
	p.rows = map[*FuncType]permissionRowSnapshot{}
	for i, decl := range decls {
		reader = i
		if ft, _ := a.recomputeFunctionPermissionEffects(decl); ft != nil {
			p.rows[ft] = snapshotPermissionRow(ft)
		}
	}
}

// solveIncremental re-evaluates the edited functions and, transitively, the
// readers of every row that changed. It reports false (rows possibly partly
// advanced, still a valid starting point) when it cannot finish.
func (p *retainedPermissions) solveIncremental(a *Analyzer, decls []scopedDecl, appended ...map[*ast.FuncDecl]*FuncType) bool {
	if !p.usable || p.rows == nil {
		return false
	}
	// Verified fresh declarations were absent from every old equation. Their
	// own validation and every rebound caller run because those bodies are edited.
	// A new row alone cannot invalidate cached validation of unrelated old bodies.
	fresh := map[*FuncType]bool{}
	for _, functions := range appended {
		for fn, verified := range functions {
			sym := a.funcDeclSymbols[fn]
			if sym == nil || sym.Type != verified || verified == nil {
				return false
			}
			if _, known := p.rows[verified]; known || len(p.readers[verified]) != 0 {
				return false
			}
			fresh[verified] = true
		}
	}
	index := map[*ast.FuncDecl]int{}
	for i, sd := range decls {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			index[fn] = i
		}
	}
	if p.initial == nil {
		return false
	}
	var work []int
	queued := map[int]bool{}
	for fn := range p.edited {
		i, ok := index[fn]
		if !ok {
			return false
		}
		work = append(work, i)
		queued[i] = true
		if sym := a.funcDeclSymbols[fn]; sym != nil {
			if ft, ok := sym.Type.(*FuncType); ok && ft != nil {
				p.initial[ft] = snapshotPermissionRow(ft)
			}
		}
	}
	before := map[*FuncType]permissionRowSnapshot{}
	for ft, row := range p.rows {
		before[ft] = row
	}
	p.changed = map[*FuncType]bool{}
	saved := a.permissionInference
	defer func() { a.permissionInference = saved }()
	reader := 0
	a.permissionInference = &permissionInferenceObserver{
		read: func(ft *FuncType) {
			if p.readers[ft] == nil {
				p.readers[ft] = map[int]bool{}
			}
			p.readers[ft][reader] = true
		},
		changed: func(*FuncType) {},
	}
	evals := 0
	shrank := false
	solve := func() bool {
		for len(work) > 0 {
			i := work[0]
			work = work[1:]
			queued[i] = false
			reader = i
			ft, _ := a.recomputeFunctionPermissionEffects(decls[i])
			evals++
			if evals > 8*len(decls)+16 {
				return false
			}
			if ft == nil {
				continue
			}
			prev, known := p.rows[ft]
			if known && prev.same(ft) {
				continue
			}
			if known && !prev.coveredBy(ft) {
				shrank = true
			}
			p.rows[ft] = snapshotPermissionRow(ft)
			if !fresh[ft] {
				p.changed[ft] = true
			}
			for r := range p.readers[ft] {
				if !queued[r] {
					queued[r] = true
					work = append(work, r)
				}
			}
		}
		return true
	}
	if !solve() {
		return false
	}
	if shrank {
		// Re-solving from the old rows keeps a lost effect alive around a call cycle
		// (each row in it still reads the other's). Everything that reads an edited row,
		// transitively, restarts from its initial row, as the full solve does, and the
		// cone is solved to its least fixpoint; rows outside it do not read the edit.
		cone := map[int]bool{}
		stack := append([]int(nil), indicesOf(p.edited, index)...)
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if cone[i] {
				continue
			}
			cone[i] = true
			ft := permissionDeclType(a, decls[i])
			if ft == nil {
				continue
			}
			for r := range p.readers[ft] {
				if !cone[r] {
					stack = append(stack, r)
				}
			}
		}
		for i := range cone {
			ft := permissionDeclType(a, decls[i])
			if ft == nil {
				continue
			}
			start, ok := p.initial[ft]
			if !ok {
				return false
			}
			start.restore(ft)
			p.rows[ft] = start
			if !queued[i] {
				queued[i] = true
				work = append(work, i)
			}
		}
		if !solve() {
			return false
		}
		// Changed is judged against the rows before this update, not the reset ones.
		p.changed = map[*FuncType]bool{}
		for i := range cone {
			if ft := permissionDeclType(a, decls[i]); ft != nil {
				if prev, known := before[ft]; !fresh[ft] && (!known || !prev.same(ft)) {
					p.changed[ft] = true
				}
			}
		}
	}
	a.permissionEffectsOutcome = permissionInferenceOutcome{Evaluations: evals, Rounds: 1, Converged: true}
	p.solvedIn = true
	return true
}

func indicesOf(fns map[*ast.FuncDecl]bool, index map[*ast.FuncDecl]int) []int {
	out := make([]int, 0, len(fns))
	for fn := range fns {
		if i, ok := index[fn]; ok {
			out = append(out, i)
		}
	}
	return out
}

// permissionDeclType is the function type whose row a permission declaration publishes.
func permissionDeclType(a *Analyzer, sd scopedDecl) *FuncType {
	fn, ok := sd.Decl.(*ast.FuncDecl)
	if !ok {
		return nil
	}
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		ft, _ := sym.Type.(*FuncType)
		return ft
	}
	return nil
}

// replay reports fn's validation diagnostics from the last run when fn was not
// edited and the incremental solve changed no row.
func (p *retainedPermissions) replay(a *Analyzer, fn *ast.FuncDecl) bool {
	if !p.solvedIn || len(p.changed) != 0 || p.edited[fn] {
		return false
	}
	diags, ok := p.diags[fn]
	if !ok {
		return false
	}
	for _, d := range diags {
		a.appendDiagnostic(d)
	}
	return true
}

// Appending equations preserves all recorded reader indexes only when the old
// declaration sequence is an exact prefix. Every old row must still be known;
// every tail node must be newly typed and included in body re-analysis.
// Replacements, removals, index shifts and shared types retain the full solve.
func (p *retainedPermissions) appendedFunctions(a *Analyzer, before []scopedDecl, beforeTypes []*FuncType, knownTypes map[*FuncType]bool, current, changed []scopedDecl) (map[*ast.FuncDecl]*FuncType, bool) {
	if p.readers == nil || p.rows == nil || p.initial == nil || knownTypes == nil || len(beforeTypes) != len(before) || len(current) <= len(before) {
		return nil, false
	}
	for i, old := range before {
		if current[i].Decl != old.Decl || current[i].Namespace != old.Namespace || !sameStringSlice(current[i].Usings, old.Usings) {
			return nil, false
		}
		ft := permissionDeclType(a, current[i])
		if ft != beforeTypes[i] {
			return nil, false
		}
		// Untyped/non-executable declarations publish no equation row. A
		// previously typed row must still be present with the exact same type.
		if ft != nil {
			if _, known := p.rows[ft]; !known {
				return nil, false
			}
		}
	}
	edited := map[ast.Decl]bool{}
	for _, sd := range changed {
		edited[sd.Decl] = true
	}
	types := map[*FuncType]bool{}
	appended := map[*ast.FuncDecl]*FuncType{}
	for _, sd := range current[len(before):] {
		fn, ok := sd.Decl.(*ast.FuncDecl)
		if !ok || !edited[fn] {
			return nil, false
		}
		ft := permissionDeclType(a, sd)
		if ft == nil || types[ft] || knownTypes[ft] || len(p.readers[ft]) != 0 {
			return nil, false
		}
		if _, known := p.rows[ft]; known {
			return nil, false
		}
		types[ft] = true
		appended[fn] = ft
	}
	return appended, true
}
