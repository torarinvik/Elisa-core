package unparse

import (
	"elisacore/src/ast"
	"strings"
)

func formatTokenSetValue(decl *ast.TokenSetDecl) string {
	if decl == nil || decl.Value == nil {
		return "[]"
	}
	typeName, ok := tokenSetFormatterQualifierName(decl.ElemType)
	if !ok || typeName == "" {
		return formatExpr(decl.Value)
	}
	elems := make([]ast.Expr, 0, len(decl.Value.Elems))
	for _, elem := range decl.Value.Elems {
		elems = append(elems, unqualifyTokenSetElemForFormat(elem, typeName))
	}
	return formatExpr(&ast.ListLitExpr{Position: decl.Value.Position, Elems: elems})
}
func tokenSetFormatterQualifierName(elemType ast.TypeExpr) (string, bool) {
	switch n := elemType.(type) {
	case *ast.NamedType:
		if n == nil || n.Name == "" {
			return "", false
		}
		return n.Name, true
	default:
		return "", false
	}
}
func unqualifyTokenSetElemForFormat(expr ast.Expr, typeName string) ast.Expr {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		if n != nil && !n.Safe && n.Field != "" {
			if ident, ok := n.Object.(*ast.Ident); ok && ident != nil && ident.Name == typeName {
				return &ast.Ident{Position: n.Position, Name: n.Field}
			}
		}
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: unqualifyTokenSetElemForFormat(n.Inner, typeName)}
	}
	return expr
}
func (f *formatter) writeGrammarInfixTableDecl(level int, table ast.GrammarInfixTableDecl) {
	f.writeLine(level, "infix table "+table.Name+"("+table.Result+"):")
	for _, levelDecl := range table.Levels {
		f.writeNamedPrecedenceLevel(level+1, levelDecl)
	}
}
func (f *formatter) writeGrammarChannelDecl(level int, channel ast.GrammarChannelDecl) {
	line := "channel " + channel.Name
	if channel.Type != nil {
		line += ": " + formatTypeExpr(channel.Type)
	}
	if channel.Default != nil {
		line += " = " + formatExpr(channel.Default)
	}
	f.writeLine(level, line)
}
func (f *formatter) writeTreeMember(level int, member ast.TreeMemberDecl) {
	if member == nil {
		return
	}
	switch n := member.(type) {
	case *ast.TreeCategoryDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "node "+treeCategoryLocalName(n.Name)+":")
		for _, variant := range n.Variants {
			f.writeLine(level+1, formatEnumVariantDecl(variant))
		}
		for i := range n.Nested {
			f.writeTreeMember(level+1, &n.Nested[i])
		}
	case *ast.TreeBlockDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "block "+n.Name+":")
		for _, field := range n.Fields {
			f.writeField(level+1, field)
		}
	case *ast.TreeStructDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "struct "+n.Name+":")
		for _, field := range n.Fields {
			f.writeField(level+1, field)
		}
	}
}
func treeCategoryLocalName(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 && idx+1 < len(name) {
		return name[idx+1:]
	}
	return name
}
func (f *formatter) writeGrammarProduction(level int, production ast.GrammarProductionDecl) {
	header := ""
	if production.Public {
		header += "pub "
	}
	header += production.Name
	if production.Append {
		header += " +="
		f.writeLine(level, header)
		for _, term := range production.Terms {
			f.writeGrammarTerm(level+1, term)
		}
		return
	}
	if production.HasParamList || len(production.Params) != 0 {
		header += "(" + formatParamList(production.Params) + ")"
	}
	if production.ReturnType != nil {
		header += " -> " + formatTypeExpr(production.ReturnType)
	}
	if production.RecoverPolicy != "" {
		header += formatGrammarRecoverPolicyUse(production.RecoverPolicy)
	} else if production.RecoverMsg != nil && len(production.RecoverUntil) != 0 {
		header += formatGrammarRecoverClause(production.RecoverMsg, production.RecoverUntil, production.RecoverValue)
	}
	header += ":"
	f.writeLine(level, header)
	for _, channel := range production.Channels {
		f.writeGrammarChannelDecl(level+1, channel)
	}
	for _, term := range production.Terms {
		f.writeGrammarTerm(level+1, term)
	}
}
func (f *formatter) writeGroupedGrammarAppendProductions(level int, productions []ast.GrammarProductionDecl) {
	if len(productions) == 0 {
		return
	}
	header := ""
	if productions[0].Public {
		header += "pub "
	}
	header += productions[0].Name + " +=:"
	f.writeLine(level, header)
	for index, production := range productions {
		if index > 0 {
			f.writeLine(level+1, "|")
		}
		for _, term := range production.Terms {
			f.writeGrammarTerm(level+1, term)
		}
	}
}
func (f *formatter) writeGrammarRecoveryDecl(level int, recovery ast.GrammarRecoveryDecl) {
	f.writeLine(level, "recovery "+recovery.Name+":")
	if recovery.Message != nil {
		f.writeLine(level+1, "message "+formatExpr(recovery.Message))
	}
	if len(recovery.Until) != 0 {
		untilParts := make([]string, 0, len(recovery.Until))
		for _, stop := range recovery.Until {
			untilParts = append(untilParts, formatGrammarTerm(stop))
		}
		f.writeLine(level+1, "until "+strings.Join(untilParts, ", "))
	}
	if recovery.Fallback != nil {
		f.writeLine(level+1, "fallback "+formatExpr(recovery.Fallback))
	}
}
func (f *formatter) writeGrammarTokenSetDecl(level int, tokenSet ast.GrammarTokenSetDecl) {
	header := "tokenset "
	if tokenSet.TokenFamily {
		header = "token family "
	}
	f.writeLine(level, header+tokenSet.Name+":")
	for _, term := range tokenSet.Terms {
		f.writeLine(level+1, formatGrammarTokenSetItem(term))
	}
}
func (f *formatter) writeGrammarAliasDecl(level int, alias ast.GrammarAliasDecl) {
	name := alias.Name
	if len(alias.Params) != 0 {
		name += "(" + strings.Join(formatGrammarFnParams(alias.Params), ", ") + ")"
	}
	if grammarTermUsesBlockForm(alias.Term) {
		f.writeLine(level, "grammar alias "+name+":")
		if seq, ok := alias.Term.(*ast.GrammarSeqTerm); ok {
			for _, term := range seq.Terms {
				f.writeGrammarTerm(level+1, term)
			}
			return
		}
		f.writeGrammarTerm(level+1, alias.Term)
		return
	}
	f.writeLine(level, "grammar alias "+name+" = "+formatGrammarTerm(alias.Term))
}
func (f *formatter) writeGrammarFnDecl(level int, grammarFn ast.GrammarFnDecl) {
	params := formatGrammarFnParams(grammarFn.Params)
	line := "grammarfn " + grammarFn.Name
	if grammarFn.Shorthand {
		line = "grammar " + grammarFn.Name
	} else if grammarFn.TypeCtor {
		line = "grammar type " + grammarFn.Name
	}
	line += formatGenericParams(grammarFn.GenericParams, grammarFn.TypeParams, nil, nil, nil, nil)
	line += "(" + strings.Join(params, ", ") + ")"
	if grammarFn.Return.Kind != "" {
		if grammarFn.Shorthand && grammarFn.Return.Kind == "grammar" && grammarFn.Return.Result != nil {
			line += " -> " + formatTypeExpr(grammarFn.Return.Result)
		} else {
			line += " -> " + formatGrammarFnType(grammarFn.Return)
		}
	}
	line += ":"
	f.writeLine(level, line)
	for _, term := range grammarFn.Terms {
		f.writeGrammarTerm(level+1, term)
	}
}
func formatGrammarFnParams(params []ast.GrammarFnParam) []string {
	formatted := make([]string, 0, len(params))
	for _, param := range params {
		text := param.Name
		if param.Type.Kind != "" {
			text += ": " + formatGrammarFnType(param.Type)
		}
		if param.Default != nil {
			text += " = " + formatGrammarTerm(param.Default)
		}
		if param.DefaultExpr != nil {
			text += " = " + formatExpr(param.DefaultExpr)
		}
		formatted = append(formatted, text)
	}
	return formatted
}
func formatGrammarFnType(typ ast.GrammarFnType) string {
	switch typ.Kind {
	case "grammar":
		if typ.Result != nil {
			return "grammar -> " + formatTypeExpr(typ.Result)
		}
		return "grammar"
	case "tokenset":
		return "tokenset"
	case "expr":
		return "expr"
	default:
		return typ.Kind
	}
}
func formatGrammarTokenSetItem(term ast.GrammarTerm) string {
	switch n := term.(type) {
	case *ast.GrammarFirstTerm:
		return "first(" + n.Name + ")"
	case *ast.GrammarTokenKindTerm:
		return n.Kind
	default:
		return formatGrammarTerm(term)
	}
}
func formatGrammarRecoverClause(message ast.Expr, until []ast.GrammarTerm, fallback ast.Expr) string {
	untilParts := make([]string, 0, len(until))
	for _, stop := range until {
		untilParts = append(untilParts, formatGrammarTerm(stop))
	}
	text := " recover(" + formatExpr(message) + ", until(" + strings.Join(untilParts, ", ") + ")"
	if fallback != nil {
		text += ", " + formatExpr(fallback)
	}
	return text + ")"
}
func formatGrammarRecoverPolicyUse(name string) string {
	return " recover " + name
}
func (f *formatter) writeGrammarTerm(level int, term ast.GrammarTerm) {
	if ret, ok := term.(*ast.GrammarReturnTerm); ok {
		if seq, ok := ret.Term.(*ast.GrammarSeqTerm); ok {
			f.writeReturnSeqTerm(level, seq)
			return
		}
		f.writeLine(level, formatGrammarTerm(ret))
		return
	}
	if bind, ok := term.(*ast.GrammarBindTerm); ok {
		if tokenKind, ok := bind.Term.(*ast.GrammarTokenKindTerm); ok {
			f.writeLine(level, "."+tokenKind.Kind+"("+bind.Name+")")
			return
		}
		if choice, ok := bind.Term.(*ast.GrammarChoiceTerm); ok && grammarChoiceUsesBlockForm(choice) {
			f.writeBoundChoiceTerm(level, bind.Name, choice)
			return
		}
		if match, ok := bind.Term.(*ast.GrammarMatchTerm); ok {
			f.writeBoundMatchTerm(level, bind.Name, match)
			return
		}
		if seq, ok := bind.Term.(*ast.GrammarSeqTerm); ok {
			f.writeBoundSeqTerm(level, bind.Name, seq)
			return
		}
		if suffix, ok := bind.Term.(*ast.GrammarSuffixTerm); ok {
			f.writeBoundSuffixTerm(level, bind.Name, suffix)
			return
		}
		if postfix, ok := bind.Term.(*ast.GrammarPostfixTerm); ok {
			f.writeBoundPostfixTerm(level, bind.Name, postfix)
			return
		}
		if precedence, ok := bind.Term.(*ast.GrammarPrecedenceTerm); ok {
			f.writeBoundPrecedenceTerm(level, bind.Name, precedence)
			return
		}
	}
	if assign, ok := term.(*ast.GrammarAssignTerm); ok {
		if choice, ok := assign.Term.(*ast.GrammarChoiceTerm); ok && grammarChoiceUsesBlockForm(choice) {
			f.writeAssignedChoiceTerm(level, assign.Name, choice)
			return
		}
		if match, ok := assign.Term.(*ast.GrammarMatchTerm); ok {
			f.writeAssignedMatchTerm(level, assign.Name, match)
			return
		}
		if seq, ok := assign.Term.(*ast.GrammarSeqTerm); ok {
			f.writeAssignedSeqTerm(level, assign.Name, seq)
			return
		}
	}
	if choice, ok := term.(*ast.GrammarChoiceTerm); ok && grammarChoiceUsesBlockForm(choice) {
		f.writeChoiceTerm(level, choice)
		return
	}
	if match, ok := term.(*ast.GrammarMatchTerm); ok {
		f.writeMatchTerm(level, match)
		return
	}
	if seq, ok := term.(*ast.GrammarSeqTerm); ok {
		if prefix, ok := formatGrammarPrefixSugar(seq); ok {
			f.writeLine(level, prefix)
			return
		}
		f.writeSeqTerm(level, seq)
		return
	}
	if suffix, ok := term.(*ast.GrammarSuffixTerm); ok {
		f.writeSuffixTerm(level, suffix)
		return
	}
	if postfix, ok := term.(*ast.GrammarPostfixTerm); ok {
		f.writePostfixTerm(level, postfix)
		return
	}
	if precedence, ok := term.(*ast.GrammarPrecedenceTerm); ok {
		f.writePrecedenceTerm(level, precedence)
		return
	}
	f.writeLine(level, formatGrammarTerm(term))
}
func grammarChoiceUsesBlockForm(choice *ast.GrammarChoiceTerm) bool {
	if choice == nil {
		return false
	}
	if len(choice.Options) > 4 {
		return true
	}
	for _, option := range choice.Options {
		switch option.(type) {
		case *ast.GrammarSeqTerm, *ast.GrammarChoiceTerm, *ast.GrammarWhenTerm, *ast.GrammarMatchTerm, *ast.GrammarSuffixTerm, *ast.GrammarPostfixTerm, *ast.GrammarPrecedenceTerm:
			return true
		}
	}
	return false
}
func grammarTermUsesBlockForm(term ast.GrammarTerm) bool {
	switch n := term.(type) {
	case *ast.GrammarChoiceTerm:
		return true
	case *ast.GrammarSeqTerm, *ast.GrammarMatchTerm, *ast.GrammarSuffixTerm, *ast.GrammarPostfixTerm, *ast.GrammarPrecedenceTerm:
		return true
	case *ast.GrammarBindTerm:
		return grammarTermUsesBlockForm(n.Term)
	case *ast.GrammarAssignTerm:
		return grammarTermUsesBlockForm(n.Term)
	default:
		return false
	}
}
func (f *formatter) writeBoundChoiceTerm(level int, name string, choice *ast.GrammarChoiceTerm) {
	if choice == nil {
		f.writeLine(level, name+" = <invalid_grammar_term>")
		return
	}
	f.writeLine(level, name+" = choice:")
	for _, option := range choice.Options {
		f.writeGrammarTerm(level+1, option)
	}
}
func (f *formatter) writeAssignedChoiceTerm(level int, name string, choice *ast.GrammarChoiceTerm) {
	if choice == nil {
		f.writeLine(level, name+" <- <invalid_grammar_term>")
		return
	}
	f.writeLine(level, name+" <- choice:")
	for _, option := range choice.Options {
		f.writeGrammarTerm(level+1, option)
	}
}
func (f *formatter) writeChoiceTerm(level int, choice *ast.GrammarChoiceTerm) {
	if choice == nil {
		f.writeLine(level, "<invalid_grammar_term>")
		return
	}
	f.writeLine(level, "choice:")
	for _, option := range choice.Options {
		f.writeGrammarTerm(level+1, option)
	}
}
func (f *formatter) writeBoundMatchTerm(level int, name string, match *ast.GrammarMatchTerm) {
	if match == nil {
		f.writeLine(level, name+" = <invalid_grammar_term>")
		return
	}
	f.writeLine(level, name+" = match "+formatExpr(match.Value)+":")
	f.writeGrammarMatchArms(level+1, match.Arms)
}
func (f *formatter) writeAssignedMatchTerm(level int, name string, match *ast.GrammarMatchTerm) {
	if match == nil {
		f.writeLine(level, name+" <- <invalid_grammar_term>")
		return
	}
	f.writeLine(level, name+" <- match "+formatExpr(match.Value)+":")
	f.writeGrammarMatchArms(level+1, match.Arms)
}
func (f *formatter) writeMatchTerm(level int, match *ast.GrammarMatchTerm) {
	if match == nil {
		f.writeLine(level, "<invalid_grammar_term>")
		return
	}
	f.writeLine(level, "match "+formatExpr(match.Value)+":")
	f.writeGrammarMatchArms(level+1, match.Arms)
}
func (f *formatter) writeGrammarMatchArms(level int, arms []ast.GrammarMatchArm) {
	for _, arm := range arms {
		patterns := make([]string, 0, len(arm.Patterns))
		for _, pattern := range arm.Patterns {
			patterns = append(patterns, formatMatchPattern(pattern))
		}
		f.writeLine(level, strings.Join(patterns, " | ")+": "+formatGrammarTerm(arm.Term))
	}
}
func (f *formatter) writeBoundSeqTerm(level int, name string, seq *ast.GrammarSeqTerm) {
	if seq == nil {
		f.writeLine(level, name+" = <invalid_grammar_term>")
		return
	}
	f.writeLine(level, name+" = seq:")
	for _, term := range seq.Terms {
		f.writeGrammarTerm(level+1, term)
	}
}
func (f *formatter) writeAssignedSeqTerm(level int, name string, seq *ast.GrammarSeqTerm) {
	if seq == nil {
		f.writeLine(level, name+" <- <invalid_grammar_term>")
		return
	}
	f.writeLine(level, name+" <- seq:")
	for _, term := range seq.Terms {
		f.writeGrammarTerm(level+1, term)
	}
}
func (f *formatter) writeSeqTerm(level int, seq *ast.GrammarSeqTerm) {
	if seq == nil {
		f.writeLine(level, "<invalid_grammar_term>")
		return
	}
	f.writeLine(level, "seq:")
	for _, term := range seq.Terms {
		f.writeGrammarTerm(level+1, term)
	}
}
func (f *formatter) writeReturnSeqTerm(level int, seq *ast.GrammarSeqTerm) {
	if seq == nil {
		f.writeLine(level, "return <invalid_grammar_term>")
		return
	}
	f.writeLine(level, "return seq:")
	for _, term := range seq.Terms {
		f.writeGrammarTerm(level+1, term)
	}
}
func (f *formatter) writeBoundSuffixTerm(level int, name string, suffix *ast.GrammarSuffixTerm) {
	if suffix == nil {
		f.writeLine(level, name+" = <invalid_grammar_term>")
		return
	}
	f.writeLine(level, name+" = suffix("+suffix.LeftName+" = "+formatGrammarTerm(suffix.Seed)+"):")
	for _, arm := range suffix.Arms {
		f.writePostfixArm(level+1, arm)
	}
}
func (f *formatter) writeSuffixTerm(level int, suffix *ast.GrammarSuffixTerm) {
	if suffix == nil {
		f.writeLine(level, "<invalid_grammar_term>")
		return
	}
	f.writeLine(level, "suffix("+suffix.LeftName+" = "+formatGrammarTerm(suffix.Seed)+"):")
	for _, arm := range suffix.Arms {
		f.writePostfixArm(level+1, arm)
	}
}
func (f *formatter) writeBoundPostfixTerm(level int, name string, postfix *ast.GrammarPostfixTerm) {
	if postfix == nil {
		f.writeLine(level, name+" = <invalid_grammar_term>")
		return
	}
	f.writeLine(level, name+" = postfix("+postfix.LeftName+" = "+formatGrammarTerm(postfix.Seed)+"):")
	for _, arm := range postfix.Arms {
		f.writePostfixArm(level+1, arm)
	}
}
func (f *formatter) writePostfixTerm(level int, postfix *ast.GrammarPostfixTerm) {
	if postfix == nil {
		f.writeLine(level, "<invalid_grammar_term>")
		return
	}
	f.writeLine(level, "postfix("+postfix.LeftName+" = "+formatGrammarTerm(postfix.Seed)+"):")
	for _, arm := range postfix.Arms {
		f.writePostfixArm(level+1, arm)
	}
}
func (f *formatter) writeBoundPrecedenceTerm(level int, name string, precedence *ast.GrammarPrecedenceTerm) {
	if precedence == nil {
		f.writeLine(level, name+" = <invalid_grammar_term>")
		return
	}
	if len(precedence.Levels) != 0 {
		f.writeLine(level, name+" = precedence("+precedence.Result+"):")
		for _, levelDecl := range precedence.Levels {
			f.writeNamedPrecedenceLevel(level+1, levelDecl)
		}
		return
	}
	header := "precedence"
	if precedence.Assoc != "" {
		header += " " + precedence.Assoc
	}
	f.writeLine(level, name+" = "+header+"("+precedence.LeftName+" = "+formatGrammarTerm(precedence.Seed)+"):")
	for _, arm := range precedence.Arms {
		f.writePrecedenceArm(level+1, arm)
	}
}
