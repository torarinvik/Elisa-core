package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type recursiveProofKind string

const (
	recursiveProofDirectNumeric   recursiveProofKind = "direct numeric"
	recursiveProofMutualNumeric   recursiveProofKind = "mutual numeric"
	recursiveProofStructuralEnum  recursiveProofKind = "structural enum"
	recursiveProofNonRecursiveDef recursiveProofKind = "non-recursive"
)

type recursiveProofCertificate struct {
	Kind    recursiveProofKind
	Caller  *ast.FuncDecl
	Callee  *ast.FuncDecl
	Call    *ast.CallExpr
	Measure ast.Expr
	Reason  string
	Outcome ProofOutcome
}

func (c recursiveProofCertificate) label() string {
	if c.Kind == "" {
		return "unknown"
	}
	return string(c.Kind)
}

func (c recursiveProofCertificate) pos() lexer.Pos {
	if c.Call != nil {
		return c.Call.Pos()
	}
	if c.Measure != nil {
		return c.Measure.Pos()
	}
	if c.Callee != nil {
		return c.Callee.Pos()
	}
	if c.Caller != nil {
		return c.Caller.Pos()
	}
	return lexer.Pos{}
}

func (a *Analyzer) directNumericTerminationCertificate(fn *ast.FuncDecl, call *ast.CallExpr) (recursiveProofCertificate, bool) {
	if a == nil || fn == nil || call == nil || len(fn.Decreases) == 0 {
		return recursiveProofCertificate{}, false
	}
	subst := a.substForSelfCall(fn, call)
	if !a.proveMeasureDecreases(fn.Decreases, subst) {
		return recursiveProofCertificate{
			Kind:    recursiveProofDirectNumeric,
			Caller:  fn,
			Callee:  fn,
			Call:    call,
			Measure: fn.Decreases[0],
			Reason:  "numeric measure did not strictly decrease",
			Outcome: ProofRefuted,
		}, false
	}
	return recursiveProofCertificate{
		Kind:    recursiveProofDirectNumeric,
		Caller:  fn,
		Callee:  fn,
		Call:    call,
		Measure: fn.Decreases[0],
		Reason:  "numeric measure strictly decreases",
		Outcome: ProofProvenLinear,
	}, true
}

func (a *Analyzer) structuralTerminationCertificate(fn *ast.FuncDecl, call *ast.CallExpr) (recursiveProofCertificate, bool) {
	cert := recursiveProofCertificate{
		Kind:    recursiveProofStructuralEnum,
		Caller:  fn,
		Callee:  fn,
		Call:    call,
		Outcome: ProofRefuted,
		Reason:  "recursive call is not on a match-bound structural child",
	}
	if a == nil || fn == nil || call == nil || len(fn.Decreases) != 1 || !a.isStructuralDecreaseMeasure(fn, fn.Decreases[0]) {
		return cert, false
	}
	cert.Measure = fn.Decreases[0]
	id, ok := fn.Decreases[0].(*ast.Ident)
	if !ok || id == nil {
		return cert, false
	}
	allowed := map[*ast.CallExpr]bool{}
	a.collectStructuralRecursiveCalls(fn.Body, id.Name, a.structuralMeasureEnumForFunc(fn, id.Name), map[string]bool{}, allowed)
	if !allowed[call] {
		return cert, false
	}
	cert.Reason = "recursive call uses a match-bound structural child"
	cert.Outcome = ProofProvenLinear
	return cert, true
}

func (a *Analyzer) recursiveCallCertificate(caller, callee *ast.FuncDecl, call *ast.CallExpr) (recursiveProofCertificate, bool) {
	if a == nil || caller == nil || callee == nil || call == nil {
		return recursiveProofCertificate{}, false
	}
	if caller == callee {
		if len(caller.Decreases) == 1 && a.isStructuralDecreaseMeasure(caller, caller.Decreases[0]) {
			return a.structuralTerminationCertificate(caller, call)
		}
		return a.directNumericTerminationCertificate(caller, call)
	}
	cert := recursiveProofCertificate{
		Kind:    recursiveProofMutualNumeric,
		Caller:  caller,
		Callee:  callee,
		Call:    call,
		Outcome: ProofRefuted,
		Reason:  "mutual edge measure did not strictly decrease",
	}
	if len(caller.Decreases) > 0 {
		cert.Measure = caller.Decreases[0]
	}
	if !a.crossFunctionMeasureDecreases(caller, callee, call) {
		return cert, false
	}
	cert.Reason = "mutual edge measure strictly decreases"
	cert.Outcome = ProofProvenLinear
	return cert, true
}

func (a *Analyzer) recursiveEquationCertificate(decl *ast.FuncDecl) (recursiveProofCertificate, bool) {
	if a == nil || decl == nil {
		return recursiveProofCertificate{}, false
	}
	edges := a.collectRecursiveSCCEdges(decl)
	if len(edges) == 0 {
		return recursiveProofCertificate{Kind: recursiveProofNonRecursiveDef, Callee: decl, Outcome: ProofProvenContract, Reason: "non-recursive pure definition"}, true
	}
	for _, edge := range edges {
		if edge.Callee != decl && edge.Caller != decl {
			continue
		}
		if cert, ok := a.recursiveCallCertificate(edge.Caller, edge.Callee, edge.Call); ok {
			cert.Outcome = ProofProvenContract
			return cert, true
		}
	}
	return recursiveProofCertificate{}, false
}
