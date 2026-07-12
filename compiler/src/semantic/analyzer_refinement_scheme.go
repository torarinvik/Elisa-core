package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// RefinementScheme is the call-boundary view of a callee's refinement/spec contract. It is an
// adapter over the canonical SpecSignature when present, with AST fallback while signatures are still
// being threaded through every declaration path.
type RefinementScheme struct {
	DeclName               string
	Params                 []ast.ParamDecl
	Requires               []ast.Expr
	EnsureValues           []ast.Expr
	IsLemma                bool
	ParamRefinements       []ParamRefinementScheme
	ReturnRefinements      []ast.RefinementPredExpr
	ParamRefinementEnsures []ParamRefinementEnsureScheme
}

type ParamRefinementScheme struct {
	ParamIndex int
	Preds      []ast.RefinementPredExpr
}

type ParamRefinementEnsureScheme struct {
	Position   lexer.Pos
	ParamIndex int
	LawName    string
	Args       []ast.Expr
}

// ValueRefinementScheme is the value-level view of refinement predicates. It is the bridge used by
// return forwarding, refined local/param reads, field reads, and return fact seeding until the
// canonical SpecSignature model can supply the same predicate list directly.
type ValueRefinementScheme struct {
	Preds []ast.RefinementPredExpr
}

func (a *Analyzer) refinementSchemeFromSignature(name string, params []ast.ParamDecl, ret ast.TypeExpr, requires []ast.Expr, ensureValues []ast.Expr, refinementEnsures []RefinementEnsure, isLemma bool) RefinementScheme {
	scheme := RefinementScheme{
		DeclName:     name,
		Params:       append([]ast.ParamDecl(nil), params...),
		Requires:     append([]ast.Expr(nil), requires...),
		EnsureValues: append([]ast.Expr(nil), ensureValues...),
		IsLemma:      isLemma,
	}
	for i, param := range params {
		rt, ok := a.paramRefinementTypeExpr(param.Type)
		if !ok || rt == nil || len(rt.Preds) == 0 {
			continue
		}
		scheme.ParamRefinements = append(scheme.ParamRefinements, ParamRefinementScheme{
			ParamIndex: i,
			Preds:      append([]ast.RefinementPredExpr(nil), rt.Preds...),
		})
	}
	if rs, ok := a.valueRefinementSchemeFromTypeExpr(ret); ok {
		scheme.ReturnRefinements = append([]ast.RefinementPredExpr(nil), rs.Preds...)
	}
	for _, re := range refinementEnsures {
		scheme.ParamRefinementEnsures = append(scheme.ParamRefinementEnsures, ParamRefinementEnsureScheme{
			Position:   re.Position,
			ParamIndex: re.ParamIndex,
			LawName:    re.LawName,
			Args:       append([]ast.Expr(nil), re.Args...),
		})
	}
	return scheme
}

func cloneRefinementScheme(s RefinementScheme) RefinementScheme {
	out := s
	out.Params = append([]ast.ParamDecl(nil), s.Params...)
	out.Requires = append([]ast.Expr(nil), s.Requires...)
	out.EnsureValues = append([]ast.Expr(nil), s.EnsureValues...)
	out.ParamRefinements = append([]ParamRefinementScheme(nil), s.ParamRefinements...)
	for i := range out.ParamRefinements {
		out.ParamRefinements[i].Preds = append([]ast.RefinementPredExpr(nil), s.ParamRefinements[i].Preds...)
	}
	out.ReturnRefinements = append([]ast.RefinementPredExpr(nil), s.ReturnRefinements...)
	out.ParamRefinementEnsures = append([]ParamRefinementEnsureScheme(nil), s.ParamRefinementEnsures...)
	for i := range out.ParamRefinementEnsures {
		out.ParamRefinementEnsures[i].Args = append([]ast.Expr(nil), s.ParamRefinementEnsures[i].Args...)
	}
	return out
}

func (a *Analyzer) callRefinementScheme(call *ast.CallExpr) (RefinementScheme, bool) {
	var canonical RefinementScheme
	var hasCanonical bool
	if ft, ok := a.callFuncType(call); ok && ft.SpecSignature != nil {
		canonical = a.refinementSchemeFromSpecSignature(ft.SpecSignature, ft.RefinementEnsures)
		hasCanonical = true
	}
	if decl, ok := a.resolveDirectCallFuncDecl(call); ok && decl != nil {
		if hasCanonical {
			scheme := cloneRefinementScheme(canonical)
			scheme.DeclName = decl.Name
			scheme.Params = append([]ast.ParamDecl(nil), decl.Params...)
			scheme.Requires = append([]ast.Expr(nil), decl.Requires...)
			scheme.IsLemma = decl.IsLemma
			return scheme, true
		}
		return a.refinementSchemeFromSignature(decl.Name, decl.Params, decl.ReturnType, decl.Requires, decl.EnsureValues, nil, decl.IsLemma), true
	}
	if ext, ok := a.resolveDirectCallExternFuncDecl(call); ok && ext != nil {
		if hasCanonical {
			scheme := cloneRefinementScheme(canonical)
			scheme.DeclName = ext.Name
			scheme.Params = append([]ast.ParamDecl(nil), ext.Params...)
			scheme.Requires = append([]ast.Expr(nil), ext.Requires...)
			return scheme, true
		}
		return a.refinementSchemeFromSignature(ext.Name, ext.Params, ext.ReturnType, ext.Requires, ext.EnsureValues, nil, false), true
	}
	return canonical, hasCanonical
}

func (a *Analyzer) callFuncType(call *ast.CallExpr) (*FuncType, bool) {
	if call == nil {
		return nil, false
	}
	if ft, ok := a.exprTypes[call.Func].(*FuncType); ok && ft != nil {
		return ft, true
	}
	if ident, ok := call.Func.(*ast.Ident); ok && ident != nil && a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(ident.Name); ok && sym != nil {
			if ft, ok := sym.Type.(*FuncType); ok && ft != nil {
				return ft, true
			}
		}
	}
	return nil, false
}

func (a *Analyzer) refinementSchemeFromSpecSignature(sig *SpecSignature, refinementEnsures []RefinementEnsure) RefinementScheme {
	scheme := RefinementScheme{
		DeclName: sig.Name,
		Params:   specSignatureParams(sig),
	}
	for _, pred := range sig.ParamPredicates {
		if pred.Subject.Kind != SpecBinderParam || pred.Subject.Position < 0 {
			continue
		}
		scheme.ParamRefinements = append(scheme.ParamRefinements, ParamRefinementScheme{
			ParamIndex: pred.Subject.Position,
			Preds: []ast.RefinementPredExpr{{
				Position: pred.Position,
				Name:     pred.LawName,
				Args:     append([]ast.Expr(nil), pred.Args...),
			}},
		})
	}
	for _, pred := range sig.ResultPredicates {
		if pred.Subject.Kind != SpecBinderResult {
			continue
		}
		scheme.ReturnRefinements = append(scheme.ReturnRefinements, ast.RefinementPredExpr{
			Position: pred.Position,
			Name:     pred.LawName,
			Args:     append([]ast.Expr(nil), pred.Args...),
		})
	}
	for _, pred := range sig.Requires {
		if pred.Kind != RefinementPredicateRequires {
			continue
		}
		if expr, ok := pred.SourceExpr.(ast.Expr); ok && expr != nil {
			scheme.Requires = append(scheme.Requires, expr)
		}
	}
	for _, pred := range sig.Ensures {
		if pred.Kind != RefinementPredicateEnsures {
			continue
		}
		if expr, ok := pred.SourceExpr.(ast.Expr); ok && expr != nil {
			scheme.EnsureValues = append(scheme.EnsureValues, expr)
		}
	}
	for _, pred := range sig.ParamPostconditionLaws {
		if pred.Subject.Kind != SpecBinderParam || pred.Subject.Position < 0 {
			continue
		}
		scheme.ParamRefinementEnsures = append(scheme.ParamRefinementEnsures, ParamRefinementEnsureScheme{
			Position:   pred.Position,
			ParamIndex: pred.Subject.Position,
			LawName:    pred.LawName,
			Args:       append([]ast.Expr(nil), pred.Args...),
		})
	}
	for _, re := range refinementEnsures {
		scheme.ParamRefinementEnsures = append(scheme.ParamRefinementEnsures, ParamRefinementEnsureScheme{
			Position:   re.Position,
			ParamIndex: re.ParamIndex,
			LawName:    re.LawName,
			Args:       append([]ast.Expr(nil), re.Args...),
		})
	}
	return scheme
}

func specSignatureParams(sig *SpecSignature) []ast.ParamDecl {
	if sig == nil || len(sig.Params) == 0 {
		return nil
	}
	params := make([]ast.ParamDecl, len(sig.Params))
	for _, binder := range sig.Params {
		if binder.Kind != SpecBinderParam || binder.Position < 0 || binder.Position >= len(params) {
			continue
		}
		params[binder.Position] = ast.ParamDecl{
			Position: binder.SourcePos,
			Name:     binder.SourceName,
			Type:     binder.SourceType,
		}
	}
	return params
}

func (a *Analyzer) valueRefinementSchemeFromTypeExpr(te ast.TypeExpr) (ValueRefinementScheme, bool) {
	rt, ok := a.paramRefinementTypeExpr(te)
	if !ok || rt == nil || len(rt.Preds) == 0 {
		return ValueRefinementScheme{}, false
	}
	return ValueRefinementScheme{Preds: rt.Preds}, true
}

func (a *Analyzer) valueRefinementSchemeEntails(s ValueRefinementScheme, pred ast.RefinementPredExpr) bool {
	for _, have := range s.Preds {
		if a.refinementPredicatesEntail(have, pred) {
			return true
		}
	}
	return false
}

func (a *Analyzer) callReturnRefinementScheme(value ast.Expr) (ValueRefinementScheme, bool) {
	call, ok := stripOptimizationParens(value).(*ast.CallExpr)
	if !ok || call == nil {
		return ValueRefinementScheme{}, false
	}
	scheme, ok := a.callRefinementScheme(call)
	if !ok || len(scheme.ReturnRefinements) == 0 {
		return ValueRefinementScheme{}, false
	}
	return ValueRefinementScheme{Preds: scheme.ReturnRefinements}, true
}

func (a *Analyzer) fieldReadRefinementScheme(value ast.Expr) (ValueRefinementScheme, bool) {
	fe, ok := stripOptimizationParens(value).(*ast.FieldExpr)
	if !ok || fe == nil {
		return ValueRefinementScheme{}, false
	}
	st, ok := stripRefForBounds(a.exprTypes[fe.Object]).(*StructType)
	if !ok || st == nil || st.Decl == nil {
		return ValueRefinementScheme{}, false
	}
	for _, fd := range st.Decl.Fields {
		if fd.Name != fe.Field {
			continue
		}
		ft := fd.Type
		if mt, mok := ft.(*ast.MutableType); mok && mt != nil {
			ft = mt.Elem
		}
		return a.valueRefinementSchemeFromTypeExpr(ft)
	}
	return ValueRefinementScheme{}, false
}

// indexElemReadRefinementScheme reports whether `value` is a darray or array INDEX read whose
// container element type carries a refinement — e.g. `xs[i]` where xs is `darray[u32 is InRange[0,127]]`.
// Sound: element refinements are enforced at every push/store site (like struct-field refinements at
// construction), so reading xs[i] inherits the InRange guarantee. Representation-erased on resolve,
// so ElemTypeExpr on DArrayType/ArrayType carries the source-level predicate.
func (a *Analyzer) indexElemReadRefinementScheme(value ast.Expr) (ValueRefinementScheme, bool) {
	idx, ok := stripOptimizationParens(value).(*ast.IndexExpr)
	if !ok || idx == nil || idx.Object == nil {
		return ValueRefinementScheme{}, false
	}
	objType := stripRefForBounds(a.exprTypes[idx.Object])
	var elemTypeExpr ast.TypeExpr
	switch at := objType.(type) {
	case *DArrayType:
		if at == nil {
			return ValueRefinementScheme{}, false
		}
		elemTypeExpr = at.ElemTypeExpr
	case *ArrayType:
		if at == nil {
			return ValueRefinementScheme{}, false
		}
		elemTypeExpr = at.ElemTypeExpr
	default:
		return ValueRefinementScheme{}, false
	}
	if elemTypeExpr == nil {
		return ValueRefinementScheme{}, false
	}
	return a.valueRefinementSchemeFromTypeExpr(elemTypeExpr)
}

func (a *Analyzer) declaredBindingRefinementScheme(value ast.Expr) (ValueRefinementScheme, bool) {
	ident, ok := stripOptimizationParens(value).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return ValueRefinementScheme{}, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Mutable {
		return ValueRefinementScheme{}, false
	}
	var declType ast.TypeExpr
	switch d := sym.Node.(type) {
	case *ast.VarDeclStmt:
		declType = d.Type
	case *ast.FuncDecl:
		if sym.Kind == SymbolParam && sym.ParamIndex >= 0 && sym.ParamIndex < len(d.Params) {
			declType = d.Params[sym.ParamIndex].Type
		}
	}
	if declType == nil {
		return ValueRefinementScheme{}, false
	}
	return a.valueRefinementSchemeFromTypeExpr(declType)
}

func (a *Analyzer) exprRefinementScheme(value ast.Expr) (ValueRefinementScheme, bool) {
	if s, ok := a.callReturnRefinementScheme(value); ok {
		return s, true
	}
	if s, ok := a.fieldReadRefinementScheme(value); ok {
		return s, true
	}
	if s, ok := a.indexElemReadRefinementScheme(value); ok {
		return s, true
	}
	return a.declaredBindingRefinementScheme(value)
}

func (a *Analyzer) exprRefinementSchemeEntails(value ast.Expr, pred ast.RefinementPredExpr) bool {
	if s, ok := a.exprRefinementScheme(value); ok && a.valueRefinementSchemeEntails(s, pred) {
		return true
	}
	// Struct-invariant composition: if `value` is a field read `s.x` and the struct type has
	// invariants that constrain `x` (e.g. `invariant self.x >= 0`), the invariant is an established
	// postcondition of every construction and every mutating method — callers may assume it. Extract
	// a numeric range from the field-specific invariant clauses and check if it entails `pred`.
	// Sound: struct invariants are enforced at construction (statically or by a debug runtime check)
	// and seeded at every method entry, so assuming them here is no stronger than the existing
	// method-entry assumption.
	return a.structInvariantEntailsFieldRefinement(value, pred)
}

// structInvariantEntailsFieldRefinement reports whether `value` is a field read `s.x` whose
// enclosing struct type has an invariant that constrains `x` in a way that entails `pred`.
// For example, a struct with `invariant self.x >= 0` lets `s.x is Nat` (where `Nat` reduces to
// `self >= 0`) discharge statically at any call site — without a runtime re-check — because the
// invariant is verified at every construction and after every field mutation.
//
// Sound: only the fragment `self.<field> OP <const>` (and conjunctions thereof) is extracted;
// anything outside that fragment is ignored, so the entailment can only succeed when the invariant
// genuinely implies the predicate. A struct WITHOUT a relevant invariant returns false, so this
// path never fabricates a guarantee.
func (a *Analyzer) structInvariantEntailsFieldRefinement(value ast.Expr, pred ast.RefinementPredExpr) bool {
	fe, ok := stripOptimizationParens(value).(*ast.FieldExpr)
	if !ok || fe == nil {
		return false
	}
	fieldRange, ok := a.structInvariantFieldRange(fe)
	if !ok {
		return false
	}
	// Check that the extracted range entails the predicate.
	lawDecl, _, ok := a.lookupLaw(pred.Name)
	if !ok {
		return false
	}
	constraints, ok := a.refinementPredicateConstraints(lawDecl, pred.Args)
	if !ok || len(constraints) == 0 {
		return false
	}
	for _, k := range constraints {
		if !rangeEntailsConstraint(fieldRange, k) {
			return false
		}
	}
	return true
}

// structInvariantFieldRange returns the numeric interval that the enclosing struct type's invariants
// pin on the specific field read `fe` (e.g. `invariant self.panel_idx >= 0` yields [0, +inf) for
// `c.panel_idx`). It intersects every invariant clause that constrains this field; ok=false when the
// object is not a struct with invariants, or no invariant clause constrains this field.
//
// Sound: a struct invariant is established at construction and re-checked after each field store (debug
// runtime), so it may be assumed at any read of the field — the same basis the refinement path
// (structInvariantEntailsFieldRefinement) and the method-entry seed already rely on.
func (a *Analyzer) structInvariantFieldRange(fe *ast.FieldExpr) (numRange, bool) {
	if fe == nil {
		return numRange{}, false
	}
	st, ok := stripRefForBounds(a.exprTypes[fe.Object]).(*StructType)
	if !ok || st == nil || len(st.Invariants) == 0 {
		return numRange{}, false
	}
	fieldRange := numRange{}
	any := false
	for _, inv := range st.Invariants {
		if inv == nil {
			continue
		}
		r, ok := a.rangeFromStructInvariantForField(inv, fe.Field)
		if !ok {
			continue
		}
		fieldRange = fieldRange.intersect(r)
		any = true
	}
	if !any {
		return numRange{}, false
	}
	return fieldRange, true
}

// rangeFromStructInvariantForField extracts a numeric interval from an invariant expression for a
// specific struct field. It recognises the simple fragments used by the range prover:
//
//   - `self.<field> OP <const>` — a direct constraint on the field
//   - `<const> OP self.<field>` — the symmetric form (operands swapped)
//   - `<left> and <right>` — conjunction: both halves are collected
//
// Everything else returns ok=false (fails closed — the caller just skips that clause).
func (a *Analyzer) rangeFromStructInvariantForField(expr ast.Expr, fieldName string) (numRange, bool) {
	var constraints []lawConstraint
	a.collectInvariantFieldConstraints(expr, fieldName, &constraints)
	if len(constraints) == 0 {
		return numRange{}, false
	}
	r, ok := rangeFromLawConstraints(constraints)
	return r, ok
}

// collectInvariantFieldConstraints walks an invariant expression collecting `self.<fieldName> OP
// const` comparisons into `out`. Conjunctions are processed recursively; clauses that do not
// involve the target field are silently skipped (not a failure).
func (a *Analyzer) collectInvariantFieldConstraints(expr ast.Expr, fieldName string, out *[]lawConstraint) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.collectInvariantFieldConstraints(n.Inner, fieldName, out)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			a.collectInvariantFieldConstraints(n.Left, fieldName, out)
			a.collectInvariantFieldConstraints(n.Right, fieldName, out)
			return
		}
		// Comparison: normalise to `field OP operand`.
		var operand ast.Expr
		var op lexer.TokenKind
		switch {
		case isSelfFieldExpr(n.Left, fieldName):
			operand, op = n.Right, n.Op
		case isSelfFieldExpr(n.Right, fieldName):
			operand, op = n.Left, flipComparison(n.Op)
		default:
			return // Clause does not involve this field — skip.
		}
		c, ok := a.operandConst(operand, nil)
		if !ok {
			return
		}
		*out = append(*out, lawConstraint{op: op, c: c})
	}
}

// isSelfFieldExpr reports whether expr is `self.<fieldName>` (an implicit-receiver field read
// from inside a struct invariant). The root of a struct invariant is always `self`.
func isSelfFieldExpr(expr ast.Expr, fieldName string) bool {
	fe, ok := expr.(*ast.FieldExpr)
	if !ok || fe == nil || fe.Field != fieldName {
		return false
	}
	root, ok := fe.Object.(*ast.Ident)
	return ok && root != nil && root.Name == "self"
}
