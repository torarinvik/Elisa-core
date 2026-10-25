package semantic

import "elisacore/src/ast"

// A published specialization retains query state sized to its owned syntax and
// queried type closure. It never retains the construction Analyzer, Retained-
// Analysis, parallel worker, or a whole-program copy of mutable fact maps.
type specializationQueries struct {
	root           *Result
	types          *Analyzer
	helpers        map[*ast.CallExpr]string
	staticBranches map[*ast.StaticIfStmt][]ast.Stmt
}

func newSpecializationQueries(source *Analyzer, root *Result, current *ast.FuncDecl, helpers map[*ast.CallExpr]string, owned map[ast.Node]bool, canonical map[ast.Node]ast.Node) *specializationQueries {
	queries := &specializationQueries{root: root, types: &Analyzer{typeShapesFrozen: true}, helpers: map[*ast.CallExpr]string{}, staticBranches: map[*ast.StaticIfStmt][]ast.Stmt{}}
	if root != nil {
		// These declaration maps are immutable program authority shared once by
		// all products. Only the tiny analyzer's predicate caches receive writes.
		queries.types.namedTypes = root.NamedTypes
		queries.types.staticImpls = root.StaticImpls
	}
	if root != nil && root.analyzer != nil {
		queries.types.programRegions = root.analyzer.programRegions
	} else if source != nil {
		queries.types.programRegions = make(map[string]bool, len(source.programRegions))
		for name, program := range source.programRegions {
			queries.types.programRegions[name] = program
		}
	}
	for call, name := range helpers {
		if owned[call] {
			queries.helpers[call] = name
		}
	}
	for node := range owned {
		if !owned[node] {
			continue
		}
		stmt, ok := node.(*ast.StaticIfStmt)
		if !ok {
			continue
		}
		// An owned static statement in an inactive enclosing arm was not
		// evaluated. Its branch is inactive too; do not re-evaluate without the
		// generic/local scope which existed during semantic analysis.
		var selected []ast.Stmt
		if source != nil {
			selected = source.specializationStaticBranches[stmt]
		}
		if selected == nil {
			queries.staticBranches[stmt] = nil
		} else {
			body := make([]ast.Stmt, len(selected))
			copy(body, selected)
			queries.staticBranches[stmt] = body
		}
	}
	// Ownership and canonical aliases live on Result, where every fact lookup
	// uses the same normalization. Do not duplicate or retain these maps here.
	_ = current
	_ = canonical
	return queries
}

func (q *specializationQueries) programLivedArgRegion(t Type) (string, bool) {
	if q == nil || q.types == nil || len(q.types.programRegions) != 1 || t == nil {
		return "", false
	}
	elem := stripRefForBounds(t)
	if !q.types.typeCanContainRegionRefs(elem, map[string]bool{}) || !q.types.typeIsProgramLivedCached(elem) {
		return "", false
	}
	for name := range q.types.programRegions {
		return name, true
	}
	return "", false
}
