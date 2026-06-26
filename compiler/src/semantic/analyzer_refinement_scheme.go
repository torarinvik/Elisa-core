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
		return RefinementScheme{
			DeclName: ext.Name,
			Params:   ext.Params,
			Requires: ext.Requires,
		}, true
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
	for _, pred := range sig.Ensures {
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
	return a.declaredBindingRefinementScheme(value)
}

func (a *Analyzer) exprRefinementSchemeEntails(value ast.Expr, pred ast.RefinementPredExpr) bool {
	s, ok := a.exprRefinementScheme(value)
	return ok && a.valueRefinementSchemeEntails(s, pred)
}
