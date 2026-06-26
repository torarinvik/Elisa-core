package semantic

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// SpecSignature is the canonical, representation-erased verification signature for a function.
// It is intentionally separate from Type equality, assignability, and layout: binders and
// predicates describe facts the verifier may assume or must discharge, not runtime shape.
type SpecSignature struct {
	Position lexer.Pos
	Name     string
	Params   []SpecBinder
	Result   *SpecBinder

	ParamPredicates        []RefinementPredicate
	ResultPredicates       []RefinementPredicate
	Requires               []RefinementPredicate
	Ensures                []RefinementPredicate
	ParamPostconditionLaws []RefinementPredicate
}

// SpecBinderKind identifies the verifier-visible role of a stable binder.
type SpecBinderKind int

const (
	SpecBinderParam SpecBinderKind = iota
	SpecBinderResult
)

// SpecBinder gives a value in a spec signature a stable identity independent of source spelling.
// Position is stable within the signature and is the primary reference used by predicates.
type SpecBinder struct {
	ID         SpecBinderID
	Kind       SpecBinderKind
	Position   int
	SourceName string
	Type       Type
	SourceType ast.TypeExpr
	SourcePos  lexer.Pos
}

// SpecBinderID is a deterministic, signature-local binder identifier.
type SpecBinderID string

// BinderRef points at a binder by stable ID and position. SourceName is diagnostic-only.
type BinderRef struct {
	ID         SpecBinderID
	Kind       SpecBinderKind
	Position   int
	SourceName string
}

// RefinementPredicateKind records which surface channel produced a canonical predicate.
type RefinementPredicateKind int

const (
	RefinementPredicateType RefinementPredicateKind = iota
	RefinementPredicateRequires
	RefinementPredicateEnsures
	RefinementPredicateLaw
)

// RefinementPredicate is one verifier predicate attached to a canonical binder.
// The subject is stored by binder reference; any other binder dependencies are listed explicitly
// so future entailment code does not need to recover them from source-name strings.
type RefinementPredicate struct {
	Position     lexer.Pos
	Kind         RefinementPredicateKind
	Subject      BinderRef
	LawName      string
	Args         []ast.Expr
	Dependencies []BinderRef
	SourceExpr   ast.Node
}

func NewSpecSignature(pos lexer.Pos, name string, params []SpecBinder, result *SpecBinder) *SpecSignature {
	return &SpecSignature{
		Position: pos,
		Name:     name,
		Params:   append([]SpecBinder(nil), params...),
		Result:   cloneSpecBinderPtr(result),
	}
}

func (a *Analyzer) buildSpecSignature(name string, params []ast.ParamDecl, paramTypes []Type, ret ast.TypeExpr, returnType Type, requires []ast.Expr, ensureValues []ast.Expr, refinementEnsures []RefinementEnsure) *SpecSignature {
	binders := make([]SpecBinder, 0, len(params))
	for i, param := range params {
		var typ Type
		if i < len(paramTypes) {
			typ = paramTypes[i]
		}
		binders = append(binders, NewParamSpecBinder(i, param.Name, typ, param.Type, param.Position))
	}
	var result *SpecBinder
	if ret != nil {
		b := NewResultSpecBinder(returnType, ret, ret.Pos())
		result = &b
	}
	pos := lexer.Pos{}
	if len(params) > 0 {
		pos = params[0].Position
	} else if ret != nil {
		pos = ret.Pos()
	}
	sig := NewSpecSignature(pos, name, binders, result)
	nameRefs := specBinderRefsByName(binders, result)
	for i, param := range params {
		rt, ok := a.paramRefinementTypeExpr(param.Type)
		if !ok || rt == nil {
			continue
		}
		subject := binders[i].Ref()
		for _, pred := range rt.Preds {
			deps := specDependencies(pred.Args, nameRefs, subject)
			sig.ParamPredicates = append(sig.ParamPredicates, NewRefinementPredicate(RefinementPredicateType, subject, pred.Name, pred.Args, deps, nil, pred.Position))
		}
	}
	if rt, ok := a.paramRefinementTypeExpr(ret); ok && rt != nil && result != nil {
		subject := result.Ref()
		for _, pred := range rt.Preds {
			deps := specDependencies(pred.Args, nameRefs, subject)
			sig.ResultPredicates = append(sig.ResultPredicates, NewRefinementPredicate(RefinementPredicateType, subject, pred.Name, pred.Args, deps, nil, pred.Position))
		}
	}
	for _, req := range requires {
		deps := specDependencies([]ast.Expr{req}, nameRefs, BinderRef{})
		sig.Requires = append(sig.Requires, NewRefinementPredicate(RefinementPredicateRequires, BinderRef{}, "", nil, deps, req, req.Pos()))
	}
	for _, ens := range ensureValues {
		subject := BinderRef{}
		if result != nil {
			subject = result.Ref()
		}
		deps := specDependencies([]ast.Expr{ens}, nameRefs, subject)
		sig.Ensures = append(sig.Ensures, NewRefinementPredicate(RefinementPredicateEnsures, subject, "", nil, deps, ens, ens.Pos()))
	}
	for _, re := range refinementEnsures {
		if re.ParamIndex < 0 || re.ParamIndex >= len(binders) {
			continue
		}
		subject := binders[re.ParamIndex].Ref()
		deps := specDependencies(re.Args, nameRefs, subject)
		sig.ParamPostconditionLaws = append(sig.ParamPostconditionLaws, NewRefinementPredicate(RefinementPredicateLaw, subject, re.LawName, re.Args, deps, nil, re.Position))
	}
	return sig
}

func NewParamSpecBinder(index int, sourceName string, typ Type, sourceType ast.TypeExpr, pos lexer.Pos) SpecBinder {
	return SpecBinder{
		ID:         paramSpecBinderID(index),
		Kind:       SpecBinderParam,
		Position:   index,
		SourceName: sourceName,
		Type:       typ,
		SourceType: sourceType,
		SourcePos:  pos,
	}
}

func NewResultSpecBinder(typ Type, sourceType ast.TypeExpr, pos lexer.Pos) SpecBinder {
	return SpecBinder{
		ID:         resultSpecBinderID(),
		Kind:       SpecBinderResult,
		Position:   0,
		SourceName: "result",
		Type:       typ,
		SourceType: sourceType,
		SourcePos:  pos,
	}
}

func NewRefinementPredicate(kind RefinementPredicateKind, subject BinderRef, lawName string, args []ast.Expr, deps []BinderRef, source ast.Node, pos lexer.Pos) RefinementPredicate {
	return RefinementPredicate{
		Position:     pos,
		Kind:         kind,
		Subject:      subject,
		LawName:      lawName,
		Args:         append([]ast.Expr(nil), args...),
		Dependencies: append([]BinderRef(nil), deps...),
		SourceExpr:   source,
	}
}

func (b SpecBinder) Ref() BinderRef {
	return BinderRef{ID: b.ID, Kind: b.Kind, Position: b.Position, SourceName: b.SourceName}
}

func specBinderRefsByName(params []SpecBinder, result *SpecBinder) map[string]BinderRef {
	out := make(map[string]BinderRef, len(params)+1)
	for _, param := range params {
		if param.SourceName != "" {
			out[param.SourceName] = param.Ref()
		}
	}
	if result != nil {
		out[result.SourceName] = result.Ref()
	}
	return out
}

func specDependencies(exprs []ast.Expr, refs map[string]BinderRef, subject BinderRef) []BinderRef {
	var out []BinderRef
	seen := map[SpecBinderID]bool{}
	if subject.ID != "" {
		seen[subject.ID] = true
	}
	var visit func(ast.Expr)
	visit = func(expr ast.Expr) {
		switch n := expr.(type) {
		case nil:
		case *ast.Ident:
			if ref, ok := refs[n.Name]; ok && !seen[ref.ID] {
				seen[ref.ID] = true
				out = append(out, ref)
			}
		case *ast.BinaryExpr:
			visit(n.Left)
			visit(n.Right)
		case *ast.UnaryExpr:
			visit(n.Operand)
		case *ast.MoveExpr:
			visit(n.Operand)
		case *ast.CallExpr:
			visit(n.Func)
			visit(n.SafeReceiver)
			for _, arg := range n.Args {
				visit(arg)
			}
		case *ast.FieldExpr:
			visit(n.Object)
		case *ast.IndexExpr:
			visit(n.Object)
			visit(n.Index)
			visit(n.Index2)
			visit(n.Fallback)
		case *ast.SliceExpr:
			visit(n.Object)
			visit(n.Start)
			visit(n.End)
		case *ast.ListLitExpr:
			for _, elem := range n.Elems {
				visit(elem)
			}
		case *ast.MembershipRangeExpr:
			visit(n.Start)
			visit(n.End)
		case *ast.CastExpr:
			visit(n.Operand)
		case *ast.TernaryExpr:
			visit(n.Value)
			visit(n.Cond)
			visit(n.Alt)
		case *ast.AddrOfExpr:
			visit(n.Operand)
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				visit(elem)
			}
		}
	}
	for _, expr := range exprs {
		visit(expr)
	}
	return out
}

func (s *SpecSignature) Param(index int) (SpecBinder, bool) {
	if s == nil || index < 0 || index >= len(s.Params) {
		return SpecBinder{}, false
	}
	return s.Params[index], true
}

func (s *SpecSignature) FindBinder(ref BinderRef) (SpecBinder, bool) {
	if s == nil {
		return SpecBinder{}, false
	}
	if ref.Kind == SpecBinderResult {
		if s.Result != nil && s.Result.ID == ref.ID {
			return *s.Result, true
		}
		return SpecBinder{}, false
	}
	if ref.Position >= 0 && ref.Position < len(s.Params) && s.Params[ref.Position].ID == ref.ID {
		return s.Params[ref.Position], true
	}
	for _, param := range s.Params {
		if param.ID == ref.ID {
			return param, true
		}
	}
	return SpecBinder{}, false
}

func cloneSpecSignature(sig *SpecSignature) *SpecSignature {
	if sig == nil {
		return nil
	}
	out := &SpecSignature{
		Position:               sig.Position,
		Name:                   sig.Name,
		Params:                 append([]SpecBinder(nil), sig.Params...),
		Result:                 cloneSpecBinderPtr(sig.Result),
		ParamPredicates:        cloneRefinementPredicates(sig.ParamPredicates),
		ResultPredicates:       cloneRefinementPredicates(sig.ResultPredicates),
		Requires:               cloneRefinementPredicates(sig.Requires),
		Ensures:                cloneRefinementPredicates(sig.Ensures),
		ParamPostconditionLaws: cloneRefinementPredicates(sig.ParamPostconditionLaws),
	}
	return out
}

// CloneSpecSignature returns a detached copy of a canonical verification signature for code outside
// the semantic package that has to rebuild/specialize FuncType values.
func CloneSpecSignature(sig *SpecSignature) *SpecSignature {
	return cloneSpecSignature(sig)
}

func cloneSpecBinderPtr(b *SpecBinder) *SpecBinder {
	if b == nil {
		return nil
	}
	out := *b
	return &out
}

func cloneRefinementPredicates(in []RefinementPredicate) []RefinementPredicate {
	if len(in) == 0 {
		return nil
	}
	out := make([]RefinementPredicate, len(in))
	for i, pred := range in {
		out[i] = pred
		out[i].Args = append([]ast.Expr(nil), pred.Args...)
		out[i].Dependencies = append([]BinderRef(nil), pred.Dependencies...)
	}
	return out
}

func paramSpecBinderID(index int) SpecBinderID {
	return SpecBinderID(fmt.Sprintf("param:%d", index))
}

func resultSpecBinderID() SpecBinderID {
	return SpecBinderID("result")
}
