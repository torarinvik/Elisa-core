package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"regexp"
)

// Proof lifetime is lexical, independently of semantic currentFuncDecl: default
// checks and quiet callee scans intentionally clear that semantic field, while a
// lambda installs a temporary synthetic FuncDecl that is not its retained owner.
func (a *Analyzer) enterIndexProofOwner(owner ast.Node, header bool) func() {
	oldOwner, oldHeader, oldContext, oldOrigin := a.currentIndexProofHeaderOwner, a.currentIndexProofHeader, a.currentIndexProofContext, a.currentIndexProofOrigin
	if fn, ok := owner.(*ast.FuncDecl); ok {
		canonical := a.canonicalFuncDecl(fn)
		if canonical == nil {
			owner = nil
		} else {
			owner = canonical
		}
	}
	a.currentIndexProofHeaderOwner, a.currentIndexProofHeader, a.currentIndexProofContext, a.currentIndexProofOrigin = owner, header, true, owner
	return func() {
		a.currentIndexProofHeaderOwner, a.currentIndexProofHeader, a.currentIndexProofContext, a.currentIndexProofOrigin = oldOwner, oldHeader, oldContext, oldOrigin
	}
}

func (a *Analyzer) inheritIndexProofOwner() func() {
	if a.currentIndexProofContext {
		return func() {}
	}
	var owner ast.Node
	if a.currentFuncDecl != nil {
		owner = a.canonicalFuncDecl(a.currentFuncDecl)
	}
	return a.enterIndexProofOwner(owner, false)
}

func (a *Analyzer) withIndexProofHeaderOwner(fn ast.Node, build func() *FuncType) *FuncType {
	defer a.beginProofContribution(fn, proofPhaseHeader, nil, "")()
	// Signature/default checks are not runtime instructions in the declared
	// function body. Keep their real telemetry and local validation checks,
	// but isolate all body effect/shape collectors from this declaration phase.
	restoreBodyObservations := a.isolateFunctionBodyObservations()
	defer restoreBodyObservations()
	restore := a.enterIndexProofOwner(fn, true)
	defer restore()
	return build()
}

// A key with nil owner is explicitly UNKNOWN, never a guessed global. Actual
// global/const initializers carry their exact declaration node. Unknown proofs
// reject retained admission; function/header/declaration proofs can be revoked.
func (a *Analyzer) recordIndexBoundsProof(expr *ast.IndexExpr) {
	if expr == nil {
		return
	}
	if a.indexBoundsProven == nil {
		a.indexBoundsProven = map[*ast.IndexExpr]bool{}
	}
	if a.indexBoundsProofOwners == nil {
		a.indexBoundsProofOwners = map[*ast.IndexExpr]ast.Node{}
	}
	if a.indexBoundsProofHeaders == nil {
		a.indexBoundsProofHeaders = map[*ast.IndexExpr]bool{}
	}
	if a.indexBoundsProofOrigins == nil {
		a.indexBoundsProofOrigins = map[*ast.IndexExpr]ast.Node{}
	}
	var owner ast.Node
	header, origin := false, a.currentIndexProofOrigin
	if a.currentIndexProofContext {
		owner, header = a.currentIndexProofHeaderOwner, a.currentIndexProofHeader
	} else if a.currentFuncDecl != nil {
		owner = a.currentFuncDecl
		origin = owner
	}
	if fn, ok := owner.(*ast.FuncDecl); ok {
		canonical := a.canonicalFuncDecl(fn)
		if canonical == nil {
			owner = nil
		} else {
			owner = canonical
		}
	}
	a.dropIndexBoundsRuntimeSite(expr)
	a.indexBoundsProven[expr] = true
	a.indexBoundsProofOwners[expr], a.indexBoundsProofHeaders[expr], a.indexBoundsProofOrigins[expr] = owner, header, origin
	if r := a.retained; r != nil && r.indexProofsUpdating {
		r.rememberFunctionIndexProof(owner, expr)
	}
}

// Keep the historical helper name for callers, but index non-function owners
// separately so declaration edits never need whole-program proof clearing.
func (r *RetainedAnalysis) rememberFunctionIndexProof(owner ast.Node, expr *ast.IndexExpr) {
	if owner == nil || expr == nil {
		return
	}
	if fn, ok := owner.(*ast.FuncDecl); ok {
		if r.indexProofsByFunc == nil {
			r.indexProofsByFunc = map[*ast.FuncDecl]map[*ast.IndexExpr]bool{}
		}
		if r.indexProofsByFunc[fn] == nil {
			r.indexProofsByFunc[fn] = map[*ast.IndexExpr]bool{}
		}
		r.indexProofsByFunc[fn][expr] = true
	} else {
		if r.indexProofsByDecl == nil {
			r.indexProofsByDecl = map[ast.Node]map[*ast.IndexExpr]bool{}
		}
		if r.indexProofsByDecl[owner] == nil {
			r.indexProofsByDecl[owner] = map[*ast.IndexExpr]bool{}
		}
		r.indexProofsByDecl[owner][expr] = true
	}
}

func (a *Analyzer) invalidateRetainedFunctionIndexBoundsProofs(fn *ast.FuncDecl) {
	if a.retained != nil && a.retained.indexProofsUpdating && !a.suppressOptimizationFacts {
		a.clearFunctionIndexBoundsProofs(fn, false)
	}
}

func (a *Analyzer) dropOwnedIndexProof(expr *ast.IndexExpr) {
	owner := a.indexBoundsProofOwners[expr]
	if owner == nil {
		owner = a.indexBoundsRuntimeSites[expr].Owner
	}
	delete(a.indexBoundsRuntimeSites, expr)
	delete(a.indexBoundsProven, expr)
	delete(a.indexBoundsProofOwners, expr)
	delete(a.indexBoundsProofHeaders, expr)
	delete(a.indexBoundsProofOrigins, expr)
	if r := a.retained; r != nil {
		if fn, ok := owner.(*ast.FuncDecl); ok {
			delete(r.indexProofsByFunc[fn], expr)
		} else {
			delete(r.indexProofsByDecl[owner], expr)
		}
	}
}

func (a *Analyzer) clearFunctionIndexBoundsProofs(fn *ast.FuncDecl, includeHeader bool) {
	if includeHeader {
		a.retractProofContributions(fn)
	}
	if fn == nil || a.retained == nil {
		return
	}
	fn = a.canonicalFuncDecl(fn)
	for expr := range a.retained.indexProofsByFunc[fn] {
		if !includeHeader && (a.indexBoundsProofHeaders[expr] || a.indexBoundsRuntimeSites[expr].Header) {
			continue
		}
		a.dropOwnedIndexProof(expr)
	}
	if len(a.retained.indexProofsByFunc[fn]) == 0 {
		delete(a.retained.indexProofsByFunc, fn)
	}
}

func (a *Analyzer) clearDeclarationIndexBoundsProofs(decl ast.Node) {
	a.retractProofContributions(decl)
	if decl == nil || a.retained == nil {
		return
	}
	if fn, ok := decl.(*ast.FuncDecl); ok {
		a.clearFunctionIndexBoundsProofs(fn, true)
		return
	}
	for expr := range a.retained.indexProofsByDecl[decl] {
		a.dropOwnedIndexProof(expr)
	}
	delete(a.retained.indexProofsByDecl, decl)
}

func retainedIndexProofDefaultsChanged(old, current *ast.StructDecl) bool {
	oldFields := map[string]ast.FieldDecl{}
	for _, field := range old.Fields {
		oldFields[field.Name] = field
	}
	text := func(expr ast.Expr) string {
		if expr == nil {
			return ""
		}
		return unparse.FormatExpr(expr)
	}
	for _, field := range current.Fields {
		previous, exists := oldFields[field.Name]
		if exists {
			if text(previous.DefaultValue) != text(field.DefaultValue) {
				return true
			}
			delete(oldFields, field.Name)
		} else if field.DefaultValue != nil {
			// New scalar zero defaults need no stale-value revalidation: the old
			// missing storage is zero-initialized at its new layout. Other defaults do.
			switch expr := stripParenExpr(field.DefaultValue).(type) {
			case *ast.IntLit:
				if expr.Value == "0" {
					continue
				}
			case *ast.BoolLit:
				if !expr.Value {
					continue
				}
			case *ast.ZeroedLit:
				continue
			}
			return true
		}
	}
	for _, field := range oldFields {
		if field.DefaultValue != nil {
			return true
		}
	}
	return false
}

func retainedIndexProofNonBodyDefaultUses(decl ast.Decl, names map[string]bool, pattern *regexp.Regexp) bool {
	matches := func(text string) bool { return retainedNameTextMatch(text, names, pattern) }
	zero := func(expr ast.Expr) bool { _, ok := stripParenExpr(expr).(*ast.ZeroedLit); return ok }
	paramUses := func(param ast.ParamDecl) bool {
		if param.DefaultValue == nil || zero(param.DefaultValue) {
			return false
		}
		return matches(retainedHeader(&ast.FuncDecl{Name: "_", Params: []ast.ParamDecl{param}}))
	}
	switch n := decl.(type) {
	case *ast.FuncDecl:
		for _, param := range n.Params {
			if paramUses(param) {
				return true
			}
		}
	case *ast.ExternFuncDecl:
		for _, param := range n.Params {
			if paramUses(param) {
				return true
			}
		}
	case *ast.GlobalDecl:
		return n.Value != nil && !zero(n.Value) && matches(unparse.FormatDecl(n))
	case *ast.ConstDecl:
		return n.Value != nil && !zero(n.Value) && matches(unparse.FormatDecl(n))
	case *ast.StructDecl:
		for _, field := range n.Fields {
			if field.DefaultValue != nil && !zero(field.DefaultValue) && matches(unparse.FormatDecl(&ast.StructDecl{Name: "_", Fields: []ast.FieldDecl{field}})) {
				return true
			}
		}
	default:
		found := false
		storeScanWalkTyped(decl, func(node any) {
			child, ok := node.(ast.Decl)
			if !ok || child == decl {
				return
			}
			switch child.(type) {
			case *ast.FuncDecl, *ast.ExternFuncDecl, *ast.StructDecl, *ast.GlobalDecl, *ast.ConstDecl:
				if retainedIndexProofNonBodyDefaultUses(child, names, pattern) {
					found = true
				}
			}
		}, map[uintptr]bool{})
		return found
	}
	return false
}
