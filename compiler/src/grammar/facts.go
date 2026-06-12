package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type grammarTermFacts struct {
	ValueType ast.TypeExpr
	CanFail   bool
	Nullable  bool
	First     []ast.GrammarTerm
}

func builtinTypeExpr(pos lexer.Pos, name string) ast.TypeExpr {
	return &ast.NamedType{Position: pos, Name: name}
}

func listTypeExpr(pos lexer.Pos, elemType ast.TypeExpr) ast.TypeExpr {
	if elemType == nil {
		elemType = builtinTypeExpr(pos, "void")
	}
	return &ast.BuiltinTypeExpr{Position: pos, Name: "darray", TypeArgs: []ast.TypeExpr{elemType}}
}

func optionalTypeExpr(pos lexer.Pos, valueType ast.TypeExpr) ast.TypeExpr {
	if valueType == nil {
		valueType = builtinTypeExpr(pos, "bool")
	}
	return &ast.OptionalTypeExpr{Position: pos, Value: valueType}
}

func nullOptionalExpr(pos lexer.Pos, valueType ast.TypeExpr) ast.Expr {
	return &ast.CastExpr{Position: pos, Operand: &ast.NullLit{Position: pos}, Target: optionalTypeExpr(pos, valueType), Origin: ast.CastExprOriginAsSyntax}
}

func presentOptionalExpr(pos lexer.Pos, value ast.Expr, valueType ast.TypeExpr) ast.Expr {
	if value == nil {
		value = zeroedCastExpr(pos, valueType)
	}
	return &ast.CastExpr{Position: pos, Operand: value, Target: optionalTypeExpr(pos, valueType), Origin: ast.CastExprOriginAsSyntax}
}

func grammarCoerceValueToType(pos lexer.Pos, value ast.Expr, valueType ast.TypeExpr, targetType ast.TypeExpr) ast.Expr {
	if value == nil {
		return zeroedCastExpr(pos, targetType)
	}
	targetOptional, targetIsOptional := targetType.(*ast.OptionalTypeExpr)
	if !targetIsOptional {
		return value
	}
	if grammarTypeExprEqual(valueType, targetType) {
		return value
	}
	if grammarTypeExprEqual(valueType, targetOptional.Value) {
		return presentOptionalExpr(pos, value, targetOptional.Value)
	}
	return value
}

func grammarListElementTypeExpr(valueType ast.TypeExpr) ast.TypeExpr {
	builtin, ok := grammarValueTypeExpr(valueType).(*ast.BuiltinTypeExpr)
	if !ok || builtin.Name != "darray" || len(builtin.TypeArgs) != 1 {
		return nil
	}
	return builtin.TypeArgs[0]
}

func grammarListElementType(pos lexer.Pos, explicit ast.TypeExpr, fallback ast.TypeExpr) ast.TypeExpr {
	if explicit != nil {
		return explicit
	}
	if inferred := grammarListElementTypeExpr(fallback); inferred != nil {
		return inferred
	}
	return builtinTypeExpr(pos, "void")
}

func (ctx *statefulLowerContext) termFacts(term ast.GrammarTerm) grammarTermFacts {
	if term == nil {
		return grammarTermFacts{}
	}
	if ctx.termFactCache != nil {
		if facts, ok := ctx.termFactCache[term]; ok {
			return facts
		}
	}
	first, nullable := grammarFirstTermsForTerm(term, ctx.productionMap, nil)
	facts := grammarTermFacts{
		ValueType: ctx.inferTermType(term),
		CanFail:   ctx.termCanFail(term),
		Nullable:  nullable,
		First:     first,
	}
	if ctx.termFactCache != nil {
		ctx.termFactCache[term] = facts
	}
	return facts
}

func (ctx *statefulLowerContext) termValueTypeOrProduction(pos lexer.Pos, term ast.GrammarTerm) ast.TypeExpr {
	if facts := ctx.termFacts(term); facts.ValueType != nil {
		return facts.ValueType
	}
	return grammarResolvedValueTypeExpr(pos, ctx.production.ReturnType)
}

func resolveGrammarFirstStop(stop ast.GrammarTerm, productions map[string]resolvedGrammarProduction, seen map[string]bool) []ast.GrammarTerm {
	first, ok := stop.(*ast.GrammarFirstTerm)
	if !ok || len(productions) == 0 {
		return []ast.GrammarTerm{stop}
	}
	if first.Name == "" {
		return nil
	}
	if seen == nil {
		seen = make(map[string]bool)
	}
	if seen[first.Name] {
		return nil
	}
	production, ok := productions[first.Name]
	if !ok {
		return []ast.GrammarTerm{stop}
	}
	seen[first.Name] = true
	terms, _ := grammarProductionFirstTerms(production.Production, productions, seen)
	delete(seen, first.Name)
	return terms
}

func grammarProductionFirstTerms(production ast.GrammarProductionDecl, productions map[string]resolvedGrammarProduction, seen map[string]bool) ([]ast.GrammarTerm, bool) {
	return grammarFirstTerms(production.Terms, productions, seen)
}

func grammarFirstTerms(terms []ast.GrammarTerm, productions map[string]resolvedGrammarProduction, seen map[string]bool) ([]ast.GrammarTerm, bool) {
	if len(terms) == 0 {
		return nil, true
	}
	resolved := make([]ast.GrammarTerm, 0)
	nullable := true
	for _, term := range terms {
		first, canBeEmpty := grammarFirstTermsForTerm(term, productions, seen)
		resolved = append(resolved, first...)
		if !canBeEmpty {
			nullable = false
			break
		}
	}
	return resolved, nullable
}

func grammarFirstTermsForTerm(term ast.GrammarTerm, productions map[string]resolvedGrammarProduction, seen map[string]bool) ([]ast.GrammarTerm, bool) {
	switch n := term.(type) {
	case nil:
		return nil, true
	case *ast.GrammarTokenTerm, *ast.GrammarTokenKindTerm:
		return []ast.GrammarTerm{term}, false
	case *ast.GrammarCallTerm:
		if _, ok := grammarTokenKindMatcher(n); ok {
			return []ast.GrammarTerm{term}, false
		}
		production, ok := productions[n.Name]
		if !ok {
			return nil, false
		}
		if seen == nil {
			seen = make(map[string]bool)
		}
		if seen[n.Name] {
			return nil, false
		}
		seen[n.Name] = true
		terms, nullable := grammarProductionFirstTerms(production.Production, productions, seen)
		delete(seen, n.Name)
		return terms, nullable
	case *ast.GrammarBindTerm:
		return grammarFirstTermsForTerm(n.Term, productions, seen)
	case *ast.GrammarAssignTerm:
		return grammarFirstTermsForTerm(n.Term, productions, seen)
	case *ast.GrammarChoiceTerm:
		resolved := make([]ast.GrammarTerm, 0)
		nullable := false
		for _, option := range n.Options {
			terms, canBeEmpty := grammarFirstTermsForTerm(option, productions, seen)
			resolved = append(resolved, terms...)
			nullable = nullable || canBeEmpty
		}
		return resolved, nullable
	case *ast.GrammarOptionalTerm:
		terms, _ := grammarFirstTermsForTerm(n.Term, productions, seen)
		return terms, true
	case *ast.GrammarWhenTerm:
		thenTerms, thenNullable := grammarFirstTermsForTerm(n.Then, productions, seen)
		elseTerms, elseNullable := grammarFirstTermsForTerm(n.Else, productions, seen)
		return append(thenTerms, elseTerms...), thenNullable || elseNullable
	case *ast.GrammarRecoverTerm:
		return grammarFirstTermsForTerm(n.Term, productions, seen)
	case *ast.GrammarRequiredTerm:
		return grammarFirstTermsForTerm(n.Term, productions, seen)
	case *ast.GrammarDelimitedTerm:
		return grammarFirstTerms([]ast.GrammarTerm{n.Open, n.Body, n.Close}, productions, seen)
	case *ast.GrammarSeqTerm:
		return grammarFirstTerms(n.Terms, productions, seen)
	case *ast.GrammarLookaheadTerm:
		return grammarFirstTermsForTerm(n.Term, productions, seen)
	case *ast.GrammarListTerm:
		return grammarFirstTermsForTerm(n.Elem, productions, seen)
	case *ast.GrammarRepeatTerm:
		terms, elemNullable := grammarFirstTermsForTerm(n.Elem, productions, seen)
		if n.MinOne {
			return terms, elemNullable
		}
		return terms, true
	case *ast.GrammarFlatRepeatTerm:
		terms, _ := grammarFirstTermsForTerm(n.Elem, productions, seen)
		return terms, true
	case *ast.GrammarWhileTerm:
		terms, _ := grammarFirstTermsForTerm(n.Elem, productions, seen)
		return terms, true
	case *ast.GrammarSeparatedTerm:
		return grammarFirstTermsForTerm(n.Elem, productions, seen)
	case *ast.GrammarSuffixTerm:
		return grammarFirstTermsForTerm(n.Seed, productions, seen)
	case *ast.GrammarPostfixTerm:
		return grammarFirstTermsForTerm(n.Seed, productions, seen)
	case *ast.GrammarPrecedenceTerm:
		if n.LeftName != "" {
			return grammarFirstTermsForTerm(n.Seed, productions, seen)
		}
		for _, level := range n.Levels {
			if level.Name == n.Result {
				return grammarFirstTermsForTerm(level.Seed, productions, seen)
			}
		}
		if len(n.Levels) != 0 {
			return grammarFirstTermsForTerm(n.Levels[0].Seed, productions, seen)
		}
		return nil, false
	case *ast.GrammarReturnTerm:
		if n.Term == nil {
			return nil, true
		}
		return grammarFirstTermsForTerm(n.Term, productions, seen)
	case *ast.GrammarExprTerm, *ast.GrammarSingletonTerm, *ast.GrammarEmptyTerm, *ast.GrammarConcatTerm, *ast.GrammarGuardTerm, *ast.GrammarAttemptTerm, *ast.GrammarCutTerm:
		return nil, true
	case *ast.GrammarTokenSetRefTerm:
		return []ast.GrammarTerm{term}, false
	case *ast.GrammarFirstTerm:
		return resolveGrammarFirstStop(n, productions, seen), false
	default:
		return nil, false
	}
}

func (ctx *statefulLowerContext) termCanFail(term ast.GrammarTerm) bool {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		return true
	case *ast.GrammarTokenKindTerm:
		return true
	case *ast.GrammarCallTerm:
		if _, ok := grammarTokenKindMatcher(n); ok {
			return true
		}
		_, production, ok := ctx.resolveGrammarProductionInfo(n)
		if ok && production.RecoverMsg != nil && len(production.RecoverUntil) != 0 {
			return false
		}
		return ok
	case *ast.GrammarChoiceTerm:
		return true
	case *ast.GrammarWhenTerm:
		return ctx.termCanFail(n.Then) || ctx.termCanFail(n.Else)
	case *ast.GrammarRequiredTerm:
		return false
	case *ast.GrammarDelimitedTerm:
		return ctx.termCanFail(n.Open) || ctx.termCanFail(n.Body)
	case *ast.GrammarSeqTerm:
		for _, term := range n.Terms {
			if ctx.termCanFail(term) {
				return true
			}
		}
		return false
	case *ast.GrammarLookaheadTerm:
		return ctx.termCanFail(n.Term)
	case *ast.GrammarExprTerm:
		return false
	case *ast.GrammarSingletonTerm:
		return false
	case *ast.GrammarEmptyTerm:
		return false
	case *ast.GrammarConcatTerm:
		for _, child := range n.Terms {
			if ctx.termCanFail(child) {
				return true
			}
		}
		return false
	case *ast.GrammarGuardTerm:
		return true
	case *ast.GrammarAttemptTerm:
		return true
	case *ast.GrammarCutTerm:
		return false
	case *ast.GrammarOptionalTerm:
		return false
	case *ast.GrammarListTerm:
		return false
	case *ast.GrammarRepeatTerm:
		return false
	case *ast.GrammarFlatRepeatTerm:
		return false
	case *ast.GrammarWhileTerm:
		return false
	case *ast.GrammarSeparatedTerm:
		return false
	case *ast.GrammarSuffixTerm:
		return ctx.termCanFail(n.Seed)
	case *ast.GrammarRecoverTerm:
		return false
	case *ast.GrammarPostfixTerm:
		return ctx.termCanFail(n.Seed)
	case *ast.GrammarPrecedenceTerm:
		return ctx.termCanFail(n.Seed)
	case *ast.GrammarBindTerm:
		return ctx.termCanFail(n.Term)
	default:
		return false
	}
}

func (ctx *statefulLowerContext) inferTermType(term ast.GrammarTerm) ast.TypeExpr {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		return ctx.tokenType
	case *ast.GrammarTokenKindTerm:
		return ctx.tokenType
	case *ast.GrammarCallTerm:
		if _, ok := grammarTokenKindMatcher(n); ok {
			return ctx.tokenType
		}
		_, production, ok := ctx.resolveGrammarProductionInfo(n)
		if ok {
			return grammarResolvedValueTypeExpr(n.Position, production.ReturnType)
		}
		return nil
	case *ast.GrammarChoiceTerm:
		var selected ast.TypeExpr
		for _, option := range n.Options {
			selected = mergeGrammarAlternativeType(selected, ctx.inferTermType(option))
		}
		return selected
	case *ast.GrammarWhenTerm:
		if empty, ok := n.Then.(*ast.GrammarEmptyTerm); ok && empty != nil && empty.Type == nil {
			return ctx.inferTermType(n.Else)
		}
		if empty, ok := n.Else.(*ast.GrammarEmptyTerm); ok && empty != nil && empty.Type == nil {
			return ctx.inferTermType(n.Then)
		}
		return mergeGrammarAlternativeType(ctx.inferTermType(n.Then), ctx.inferTermType(n.Else))
	case *ast.GrammarRequiredTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarDelimitedTerm:
		return ctx.inferTermType(n.Body)
	case *ast.GrammarSeqTerm:
		if len(n.Terms) == 0 {
			return builtinTypeExpr(n.Position, "bool")
		}
		last := n.Terms[len(n.Terms)-1]
		if assign, ok := last.(*ast.GrammarAssignTerm); ok && ctx.isChannelName(assign.Name) {
			return grammarResolvedValueTypeExpr(n.Position, ctx.production.ReturnType)
		}
		return ctx.inferTermType(last)
	case *ast.GrammarLookaheadTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarExprTerm:
		return n.Type
	case *ast.GrammarSingletonTerm:
		return listTypeExpr(n.Position, grammarListElementType(n.Position, n.Type, grammarResolvedValueTypeExpr(n.Position, ctx.production.ReturnType)))
	case *ast.GrammarEmptyTerm:
		return listTypeExpr(n.Position, grammarListElementType(n.Position, n.Type, grammarResolvedValueTypeExpr(n.Position, ctx.production.ReturnType)))
	case *ast.GrammarConcatTerm:
		return ctx.inferConcatTermType(n)
	case *ast.GrammarRecoverTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarGuardTerm:
		return builtinTypeExpr(n.Position, "bool")
	case *ast.GrammarAttemptTerm:
		return nil
	case *ast.GrammarCutTerm:
		return builtinTypeExpr(n.Position, "bool")
	case *ast.GrammarOptionalTerm:
		return optionalTypeExpr(n.Position, ctx.termFacts(n.Term).ValueType)
	case *ast.GrammarListTerm:
		return listTypeExpr(n.Position, ctx.inferTermType(n.Elem))
	case *ast.GrammarRepeatTerm:
		return listTypeExpr(n.Position, ctx.inferTermType(n.Elem))
	case *ast.GrammarFlatRepeatTerm:
		return ctx.inferTermType(n.Elem)
	case *ast.GrammarWhileTerm:
		return ctx.inferTermType(n.Elem)
	case *ast.GrammarSeparatedTerm:
		return listTypeExpr(n.Position, ctx.inferTermType(n.Elem))
	case *ast.GrammarSuffixTerm:
		return ctx.inferTermType(n.Seed)
	case *ast.GrammarPostfixTerm:
		return ctx.inferTermType(n.Seed)
	case *ast.GrammarPrecedenceTerm:
		if len(n.Levels) != 0 {
			return grammarResolvedValueTypeExpr(n.Position, ctx.production.ReturnType)
		}
		return ctx.inferTermType(n.Seed)
	case *ast.GrammarBindTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarReturnTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarPassTerm:
		return grammarResolvedValueTypeExpr(n.Position, ctx.production.ReturnType)
	}
	return grammarResolvedValueTypeExpr(ctx.production.Position, ctx.production.ReturnType)
}

func (ctx *statefulLowerContext) inferConcatTermType(term *ast.GrammarConcatTerm) ast.TypeExpr {
	if term == nil {
		return grammarResolvedValueTypeExpr(ctx.production.Position, ctx.production.ReturnType)
	}
	productionType := grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	var resultElem ast.TypeExpr
	var firstListType ast.TypeExpr
	for _, child := range term.Terms {
		childType := ctx.inferTermType(child)
		if childType == nil {
			continue
		}
		childElem := grammarListElementTypeExpr(childType)
		if childElem == nil {
			if firstListType == nil {
				firstListType = childType
			}
			continue
		}
		if firstListType == nil {
			firstListType = childType
		}
		resultElem = mergeGrammarAlternativeType(resultElem, childElem)
	}
	if resultElem != nil {
		return listTypeExpr(term.Position, resultElem)
	}
	if firstListType != nil {
		return firstListType
	}
	return productionType
}

func mergeGrammarBranchTypes(left ast.TypeExpr, right ast.TypeExpr) ast.TypeExpr {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	leftOptional, leftIsOptional := left.(*ast.OptionalTypeExpr)
	rightOptional, rightIsOptional := right.(*ast.OptionalTypeExpr)
	if leftIsOptional && rightIsOptional {
		if grammarTypeExprEqual(leftOptional.Value, rightOptional.Value) {
			return left
		}
	}
	if leftIsOptional && grammarTypeExprEqual(leftOptional.Value, right) {
		return left
	}
	if rightIsOptional && grammarTypeExprEqual(left, rightOptional.Value) {
		return right
	}
	return nil
}

func mergeGrammarAlternativeType(left ast.TypeExpr, right ast.TypeExpr) ast.TypeExpr {
	if typ := mergeGrammarBranchTypes(left, right); typ != nil {
		return typ
	}
	if left != nil {
		return left
	}
	return right
}

func grammarTypeExprEqual(left ast.TypeExpr, right ast.TypeExpr) bool {
	switch l := left.(type) {
	case *ast.NamedType:
		r, ok := right.(*ast.NamedType)
		return ok && l.Name == r.Name
	case *ast.GenericType:
		r, ok := right.(*ast.GenericType)
		if !ok || l.Name != r.Name || len(l.Args) != len(r.Args) {
			return false
		}
		for i := range l.Args {
			if !grammarTypeExprEqual(l.Args[i], r.Args[i]) {
				return false
			}
		}
		return true
	case *ast.BuiltinTypeExpr:
		r, ok := right.(*ast.BuiltinTypeExpr)
		if !ok || l.Name != r.Name || len(l.TypeArgs) != len(r.TypeArgs) || len(l.ValueArgs) != len(r.ValueArgs) {
			return false
		}
		for i := range l.TypeArgs {
			if !grammarTypeExprEqual(l.TypeArgs[i], r.TypeArgs[i]) {
				return false
			}
		}
		for i := range l.ValueArgs {
			if !grammarExprEqual(l.ValueArgs[i], r.ValueArgs[i]) {
				return false
			}
		}
		return true
	case *ast.OptionalTypeExpr:
		r, ok := right.(*ast.OptionalTypeExpr)
		return ok && grammarTypeExprEqual(l.Value, r.Value)
	case *ast.TupleTypeExpr:
		r, ok := right.(*ast.TupleTypeExpr)
		if !ok || len(l.Fields) != len(r.Fields) {
			return false
		}
		for i := range l.Fields {
			if l.Fields[i].Name != r.Fields[i].Name || !grammarTypeExprEqual(l.Fields[i].Type, r.Fields[i].Type) {
				return false
			}
		}
		return true
	case *ast.RefType:
		r, ok := right.(*ast.RefType)
		return ok && l.State == r.State && l.Storage == r.Storage && l.Region == r.Region && grammarTypeExprEqual(l.Elem, r.Elem)
	case *ast.MutableType:
		r, ok := right.(*ast.MutableType)
		return ok && grammarTypeExprEqual(l.Elem, r.Elem)
	case *ast.TailType:
		r, ok := right.(*ast.TailType)
		return ok && grammarTypeExprEqual(l.Elem, r.Elem)
	case *ast.ArrayType:
		r, ok := right.(*ast.ArrayType)
		return ok && grammarTypeExprEqual(l.Elem, r.Elem) && grammarExprEqual(l.Size, r.Size)
	case *ast.AggregateStateTypeExpr:
		r, ok := right.(*ast.AggregateStateTypeExpr)
		if !ok || l.State != r.State || len(l.States) != len(r.States) || !grammarTypeExprEqual(l.Base, r.Base) {
			return false
		}
		for i := range l.States {
			if l.States[i] != r.States[i] {
				return false
			}
		}
		return true
	default:
		return false
	}
}
