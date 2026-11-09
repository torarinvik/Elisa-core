package ast

import "elisacore/src/lexer"

func (n *ConstDecl) Pos() lexer.Pos    { return n.Position }
func (n *TokenSetDecl) Pos() lexer.Pos { return n.Position }
func (n *CharsetDecl) Pos() lexer.Pos  { return n.Position }
func (n *LayoutDecl) Pos() lexer.Pos   { return n.Position }
func (n *KeywordMapDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *KeywordMapEntry) Pos() lexer.Pos {
	return n.Position
}
func (n *ConstEnumDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *ConstEnumMemberDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *ErrorDecl) Pos() lexer.Pos      { return n.Position }
func (n *PermissionDecl) Pos() lexer.Pos { return n.Position }
func (n *AliasDecl) Pos() lexer.Pos      { return n.Position }
func (n *NamespaceDecl) Pos() lexer.Pos  { return n.Position }
func (n *UsingDecl) Pos() lexer.Pos      { return n.Position }
func (n *ImportDecl) Pos() lexer.Pos     { return n.Position }
func (n *EnumDecl) Pos() lexer.Pos       { return n.Position }
func (n *GrammarDecl) Pos() lexer.Pos    { return n.Position }
func (n *GrammarEnvDecl) Pos() lexer.Pos { return n.Position }
func (n *LexerDecl) Pos() lexer.Pos      { return n.Position }
func (n *GrammarProductionDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarPassTerm) Pos() lexer.Pos  { return n.Position }
func (n *GrammarTokenTerm) Pos() lexer.Pos { return n.Position }
func (n *GrammarTokenKindTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarCallTerm) Pos() lexer.Pos { return n.Position }
func (n *GrammarChoiceTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarOptionalTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarWhenTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarMatchTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarRecoverTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarRequiredTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarDelimitedTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarSeqTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarLookaheadTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarExprTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarSingletonTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarEmptyTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarConcatTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarGuardTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarAttemptTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarCutTerm) Pos() lexer.Pos  { return n.Position }
func (n *GrammarListTerm) Pos() lexer.Pos { return n.Position }
func (n *GrammarRepeatTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarDynamicClimbTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarFlatRepeatTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarSeparatedTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarSuffixTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarPostfixTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarPrecedenceTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarInfixTableTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarTokenSetRefTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarFirstTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GrammarApplyTerm) Pos() lexer.Pos { return n.Position }
func (n *GrammarBindTerm) Pos() lexer.Pos  { return n.Position }
func (n *GrammarAssignTerm) Pos() lexer.Pos {
	return n.Position
}
