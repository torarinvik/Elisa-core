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
	changed  map[*FuncType]bool // rows the last incremental solve changed
	edited   map[*ast.FuncDecl]bool
	usable   bool // this update may run incrementally (no added/removed functions)
	solvedIn bool // the incremental solve ran in this update
	// rows holds every row as the last solve left it: body analysis can rewrite
	// an edited function's row before the solve runs, so "changed" is judged
	// against this, not against the row the recompute found.
	rows map[*FuncType]permissionRowSnapshot
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
func (p *retainedPermissions) solveIncremental(a *Analyzer, decls []scopedDecl) bool {
	if !p.usable || p.rows == nil {
		return false
	}
	index := map[*ast.FuncDecl]int{}
	for i, sd := range decls {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			index[fn] = i
		}
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
	for len(work) > 0 {
		i := work[0]
		work = work[1:]
		queued[i] = false
		reader = i
		ft, _ := a.recomputeFunctionPermissionEffects(decls[i])
		evals++
		if evals > 4*len(decls)+16 {
			return false
		}
		if ft == nil {
			continue
		}
		prev, known := p.rows[ft]
		if known && prev.same(ft) {
			continue
		}
		p.rows[ft] = snapshotPermissionRow(ft)
		p.changed[ft] = true
		for r := range p.readers[ft] {
			if !queued[r] {
				queued[r] = true
				work = append(work, r)
			}
		}
	}
	a.permissionEffectsOutcome = permissionInferenceOutcome{Evaluations: evals, Rounds: 1, Converged: true}
	p.solvedIn = true
	return true
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
