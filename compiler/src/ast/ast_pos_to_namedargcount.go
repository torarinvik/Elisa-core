package ast

import "llcontext/src/lexer"

func (n *GrammarReturnTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *AttributeDecl) Pos() lexer.Pos    { return n.Position }
func (n *TreeCategoryDecl) Pos() lexer.Pos { return n.Position }
func (n *TreeBlockDecl) Pos() lexer.Pos    { return n.Position }
func (n *TreeStructDecl) Pos() lexer.Pos   { return n.Position }
func (n *GlobalDecl) Pos() lexer.Pos       { return n.Position }
func (n *StructDecl) Pos() lexer.Pos       { return n.Position }
func (n *StoreDecl) Pos() lexer.Pos        { return n.Position }
func (n *FuncDecl) Pos() lexer.Pos         { return n.Position }
func (n *ExternFuncDecl) Pos() lexer.Pos   { return n.Position }
func (n *ExternVarDecl) Pos() lexer.Pos    { return n.Position }
func (n *ExternTypeDecl) Pos() lexer.Pos   { return n.Position }
func (n *TypeAliasDecl) Pos() lexer.Pos    { return n.Position }
func (n *ExportTypeDecl) Pos() lexer.Pos   { return n.Position }
func (n *ExportFuncDecl) Pos() lexer.Pos   { return n.Position }
func (n *ExportGlobalDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *StaticIfDecl) Pos() lexer.Pos { return n.Position }
func (n *NamedType) Pos() lexer.Pos    { return n.Position }
func (n *RefType) Pos() lexer.Pos      { return n.Position }
func (n *RefStateLiteralTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *RefStorageLiteralTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *GenericType) Pos() lexer.Pos { return n.Position }
func (n *AggregateStateTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *StateSetTypeExpr) Pos() lexer.Pos { return n.Position }
func (n *MutableType) Pos() lexer.Pos      { return n.Position }
func (n *TailType) Pos() lexer.Pos         { return n.Position }
func (n *ArrayType) Pos() lexer.Pos        { return n.Position }
func (n *BuiltinTypeExpr) Pos() lexer.Pos  { return n.Position }
func (n *FuncTypeExpr) Pos() lexer.Pos     { return n.Position }
func (n *ErrorSetExpr) Pos() lexer.Pos     { return n.Position }
func (n *ErrorUnionTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *OptionalTypeExpr) Pos() lexer.Pos { return n.Position }
func (n *TupleTypeExpr) Pos() lexer.Pos    { return n.Position }
func (n *Ident) Pos() lexer.Pos            { return n.Position }
func (n *IntLit) Pos() lexer.Pos           { return n.Position }
func (n *FloatLit) Pos() lexer.Pos         { return n.Position }
func (n *StringLit) Pos() lexer.Pos        { return n.Position }
func (n *CharLit) Pos() lexer.Pos          { return n.Position }
func (n *BoolLit) Pos() lexer.Pos          { return n.Position }
func (n *NullLit) Pos() lexer.Pos          { return n.Position }
func (n *ZeroedLit) Pos() lexer.Pos        { return n.Position }
func (n *ExprBlock) Pos() lexer.Pos        { return n.Position }
func (n *BinaryExpr) Pos() lexer.Pos       { return n.Position }
func (n *UnaryExpr) Pos() lexer.Pos        { return n.Position }
func (n *MoveExpr) Pos() lexer.Pos         { return n.Position }
func (n *CallExpr) Pos() lexer.Pos         { return n.Position }
func (n *FieldExpr) Pos() lexer.Pos        { return n.Position }
func (n *ShorthandMemberExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *IndexExpr) Pos() lexer.Pos   { return n.Position }
func (n *SliceExpr) Pos() lexer.Pos   { return n.Position }
func (n *ListLitExpr) Pos() lexer.Pos { return n.Position }
func (n *ListComprehensionExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *QueryExpr) Pos() lexer.Pos      { return n.Position }
func (n *CastExpr) Pos() lexer.Pos       { return n.Position }
func (n *CascadeExpr) Pos() lexer.Pos    { return n.Position }
func (n *LambdaExpr) Pos() lexer.Pos     { return n.Position }
func (n *SizeofExpr) Pos() lexer.Pos     { return n.Position }
func (n *TernaryExpr) Pos() lexer.Pos    { return n.Position }
func (n *AddrOfExpr) Pos() lexer.Pos     { return n.Position }
func (n *SpecializeExpr) Pos() lexer.Pos { return n.Position }
func (n *StructLitExpr) Pos() lexer.Pos  { return n.Position }
func (n *RecordUpdateExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *TupleExpr) Pos() lexer.Pos       { return n.Position }
func (n *VariantTestExpr) Pos() lexer.Pos { return n.Position }
func (n *StructTestExpr) Pos() lexer.Pos  { return n.Position }
func (n *IsPatternExpr) Pos() lexer.Pos   { return n.Position }
func (n *TypeExprExpr) Pos() lexer.Pos    { return n.Position }
func (n *ParenExpr) Pos() lexer.Pos       { return n.Position }
func (n *RaiseExpr) Pos() lexer.Pos       { return n.Position }
func (n *TryExpr) Pos() lexer.Pos         { return n.Position }
func (n *CatchExpr) Pos() lexer.Pos       { return n.Position }
func (n *UnwrapElseExpr) Pos() lexer.Pos  { return n.Position }
func (n *OptionalBindExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *AllocExpr) Pos() lexer.Pos { return n.Position }
func (n *CanExpr) Pos() lexer.Pos   { return n.Position }
func (n *MatchExpr) Pos() lexer.Pos { return n.Position }
func (n *VisitExpr) Pos() lexer.Pos { return n.Position }
func (n *FoldExpr) Pos() lexer.Pos  { return n.Position }
func (n *EmitExpr) Pos() lexer.Pos  { return n.Position }
func (n *MatchWildcardPattern) Pos() lexer.Pos {
	return n.Position
}
func (n *MatchBindPattern) Pos() lexer.Pos { return n.Position }
func (n *MatchStringLiteralPattern) Pos() lexer.Pos {
	return n.Position
}
func (n *MatchLiteralPattern) Pos() lexer.Pos { return n.Position }
func (n *MatchTuplePattern) Pos() lexer.Pos   { return n.Position }
func (n *MatchListPattern) Pos() lexer.Pos    { return n.Position }
func (n *MatchRestPattern) Pos() lexer.Pos    { return n.Position }
func (n *MatchStructPattern) Pos() lexer.Pos  { return n.Position }
func (n *MatchVariantPattern) Pos() lexer.Pos { return n.Position }
func (n *MoveBindNamePattern) Pos() lexer.Pos { return n.Position }
func (n *MoveBindStructPattern) Pos() lexer.Pos {
	return n.Position
}
func (n *MoveBindTuplePattern) Pos() lexer.Pos      { return n.Position }
func (n *MoveBindVariantPattern) Pos() lexer.Pos    { return n.Position }
func (n *AssignStmt) Pos() lexer.Pos                { return n.Position }
func (n *AugAssignStmt) Pos() lexer.Pos             { return n.Position }
func (n *AsRefAssignStmt) Pos() lexer.Pos           { return n.Position }
func (n *VarDeclStmt) Pos() lexer.Pos               { return n.Position }
func (n *LocalParamsStmt) Pos() lexer.Pos           { return n.Position }
func (n *LetDestructureStmt) Pos() lexer.Pos        { return n.Position }
func (n *TupleBindStmt) Pos() lexer.Pos             { return n.Position }
func (n *MoveBindStmt) Pos() lexer.Pos              { return n.Position }
func (n *DeferStmt) Pos() lexer.Pos                 { return n.Position }
func (n *ReturnStmt) Pos() lexer.Pos                { return n.Position }
func (n *IfStmt) Pos() lexer.Pos                    { return n.Position }
func (n *WhileStmt) Pos() lexer.Pos                 { return n.Position }
func (n *ForStmt) Pos() lexer.Pos                   { return n.Position }
func (n *IterForStmt) Pos() lexer.Pos               { return n.Position }
func (n *ParallelForStmt) Pos() lexer.Pos           { return n.Position }
func (n *MatchStmt) Pos() lexer.Pos                 { return n.Position }
func (n *ExpectPatternStmt) Pos() lexer.Pos         { return n.Position }
func (n *InStoreStmt) Pos() lexer.Pos               { return n.Position }
func (n *CanStmt) Pos() lexer.Pos                   { return n.Position }
func (n *WithStmt) Pos() lexer.Pos                  { return n.Position }
func (n *ArgsScopeStmt) Pos() lexer.Pos             { return n.Position }
func (n *CascadeStmt) Pos() lexer.Pos               { return n.Position }
func (n *PoolStmt) Pos() lexer.Pos                  { return n.Position }
func (n *LockStmt) Pos() lexer.Pos                  { return n.Position }
func (n *PassStmt) Pos() lexer.Pos                  { return n.Position }
func (n *SignalStmt) Pos() lexer.Pos                { return n.Position }
func (n *PanicStmt) Pos() lexer.Pos                 { return n.Position }
func (n *ExprStmt) Pos() lexer.Pos                  { return n.Position }
func (n *StaticIfStmt) Pos() lexer.Pos              { return n.Position }
func (n *StaticErrorStmt) Pos() lexer.Pos           { return n.Position }
func (n *DiscardStmt) Pos() lexer.Pos               { return n.Position }
func (n *RegionStmt) Pos() lexer.Pos                { return n.Position }
func (n *DestroyStmt) Pos() lexer.Pos               { return n.Position }
func (n *MarkStmt) Pos() lexer.Pos                  { return n.Position }
func (n *CheckpointStmt) Pos() lexer.Pos            { return n.Position }
func (n *GroupedCheckpointStmt) Pos() lexer.Pos     { return n.Position }
func (n *RestoreStmt) Pos() lexer.Pos               { return n.Position }
func (n *RestoreCheckpointStmt) Pos() lexer.Pos     { return n.Position }
func (n *ResetStmt) Pos() lexer.Pos                 { return n.Position }
func (n *ScopeStmt) Pos() lexer.Pos                 { return n.Position }
func (*ConstDecl) nodeTag()                         {}
func (*TokenSetDecl) nodeTag()                      {}
func (*ConstEnumDecl) nodeTag()                     {}
func (*ConstEnumMemberDecl) nodeTag()               {}
func (*ErrorDecl) nodeTag()                         {}
func (*EffectsDecl) nodeTag()                       {}
func (*EffectDecl) nodeTag()                        {}
func (*PermissionDecl) nodeTag()                    {}
func (*ContextDecl) nodeTag()                       {}
func (*ParamsDecl) nodeTag()                        {}
func (*NamespaceDecl) nodeTag()                     {}
func (*UsingDecl) nodeTag()                         {}
func (*EnumDecl) nodeTag()                          {}
func (*TreeDecl) nodeTag()                          {}
func (*GrammarDecl) nodeTag()                       {}
func (*GrammarEnvDecl) nodeTag()                    {}
func (*LexerDecl) nodeTag()                         {}
func (*GrammarProductionDecl) nodeTag()             {}
func (*GrammarPassTerm) nodeTag()                   {}
func (*GrammarTokenTerm) nodeTag()                  {}
func (*GrammarTokenKindTerm) nodeTag()              {}
func (*GrammarCallTerm) nodeTag()                   {}
func (*GrammarChoiceTerm) nodeTag()                 {}
func (*GrammarOptionalTerm) nodeTag()               {}
func (*GrammarWhenTerm) nodeTag()                   {}
func (*GrammarMatchTerm) nodeTag()                  {}
func (*GrammarRecoverTerm) nodeTag()                {}
func (*GrammarRequiredTerm) nodeTag()               {}
func (*GrammarDelimitedTerm) nodeTag()              {}
func (*GrammarSeqTerm) nodeTag()                    {}
func (*GrammarLookaheadTerm) nodeTag()              {}
func (*GrammarExprTerm) nodeTag()                   {}
func (*GrammarSingletonTerm) nodeTag()              {}
func (*GrammarEmptyTerm) nodeTag()                  {}
func (*GrammarConcatTerm) nodeTag()                 {}
func (*GrammarGuardTerm) nodeTag()                  {}
func (*GrammarAttemptTerm) nodeTag()                {}
func (*GrammarCutTerm) nodeTag()                    {}
func (*GrammarListTerm) nodeTag()                   {}
func (*GrammarRepeatTerm) nodeTag()                 {}
func (*GrammarFlatRepeatTerm) nodeTag()             {}
func (*GrammarWhileTerm) nodeTag()                  {}
func (*GrammarSeparatedTerm) nodeTag()              {}
func (*GrammarSuffixTerm) nodeTag()                 {}
func (*GrammarPostfixTerm) nodeTag()                {}
func (*GrammarPrecedenceTerm) nodeTag()             {}
func (*GrammarInfixTableTerm) nodeTag()             {}
func (*GrammarTokenSetRefTerm) nodeTag()            {}
func (*GrammarFirstTerm) nodeTag()                  {}
func (*GrammarApplyTerm) nodeTag()                  {}
func (*GrammarBindTerm) nodeTag()                   {}
func (*GrammarAssignTerm) nodeTag()                 {}
func (*GrammarReturnTerm) nodeTag()                 {}
func (*AttributeDecl) nodeTag()                     {}
func (*TreeCategoryDecl) nodeTag()                  {}
func (*TreeBlockDecl) nodeTag()                     {}
func (*TreeStructDecl) nodeTag()                    {}
func (*GlobalDecl) nodeTag()                        {}
func (*StructDecl) nodeTag()                        {}
func (*StoreDecl) nodeTag()                         {}
func (*FuncDecl) nodeTag()                          {}
func (*ExternFuncDecl) nodeTag()                    {}
func (*ExternVarDecl) nodeTag()                     {}
func (*ExternTypeDecl) nodeTag()                    {}
func (*ExportTypeDecl) nodeTag()                    {}
func (*ExportFuncDecl) nodeTag()                    {}
func (*ExportGlobalDecl) nodeTag()                  {}
func (*StaticIfDecl) nodeTag()                      {}
func (*NamedType) nodeTag()                         {}
func (*RefType) nodeTag()                           {}
func (*RefStateLiteralTypeExpr) nodeTag()           {}
func (*RefStorageLiteralTypeExpr) nodeTag()         {}
func (*GenericType) nodeTag()                       {}
func (*AggregateStateTypeExpr) nodeTag()            {}
func (*StateSetTypeExpr) nodeTag()                  {}
func (*MutableType) nodeTag()                       {}
func (*TailType) nodeTag()                          {}
func (*ArrayType) nodeTag()                         {}
func (*BuiltinTypeExpr) nodeTag()                   {}
func (*FuncTypeExpr) nodeTag()                      {}
func (*ErrorSetExpr) nodeTag()                      {}
func (*ErrorUnionTypeExpr) nodeTag()                {}
func (*OptionalTypeExpr) nodeTag()                  {}
func (*TupleTypeExpr) nodeTag()                     {}
func (*Ident) nodeTag()                             {}
func (*IntLit) nodeTag()                            {}
func (*FloatLit) nodeTag()                          {}
func (*StringLit) nodeTag()                         {}
func (*CharLit) nodeTag()                           {}
func (*BoolLit) nodeTag()                           {}
func (*NullLit) nodeTag()                           {}
func (*ZeroedLit) nodeTag()                         {}
func (*ExprBlock) nodeTag()                         {}
func (*BinaryExpr) nodeTag()                        {}
func (*UnaryExpr) nodeTag()                         {}
func (*MoveExpr) nodeTag()                          {}
func (*CallExpr) nodeTag()                          {}
func (*FieldExpr) nodeTag()                         {}
func (*ShorthandMemberExpr) nodeTag()               {}
func (*IndexExpr) nodeTag()                         {}
func (*SliceExpr) nodeTag()                         {}
func (*ListLitExpr) nodeTag()                       {}
func (*ListComprehensionExpr) nodeTag()             {}
func (*QueryExpr) nodeTag()                         {}
func (*CastExpr) nodeTag()                          {}
func (*CascadeExpr) nodeTag()                       {}
func (*LambdaExpr) nodeTag()                        {}
func (*SizeofExpr) nodeTag()                        {}
func (*TernaryExpr) nodeTag()                       {}
func (*AddrOfExpr) nodeTag()                        {}
func (*SpecializeExpr) nodeTag()                    {}
func (*StructLitExpr) nodeTag()                     {}
func (*RecordUpdateExpr) nodeTag()                  {}
func (*TupleExpr) nodeTag()                         {}
func (*VariantTestExpr) nodeTag()                   {}
func (*StructTestExpr) nodeTag()                    {}
func (*IsPatternExpr) nodeTag()                     {}
func (*TypeExprExpr) nodeTag()                      {}
func (*ParenExpr) nodeTag()                         {}
func (*RaiseExpr) nodeTag()                         {}
func (*TryExpr) nodeTag()                           {}
func (*CatchExpr) nodeTag()                         {}
func (*UnwrapElseExpr) nodeTag()                    {}
func (*OptionalBindExpr) nodeTag()                  {}
func (*AllocExpr) nodeTag()                         {}
func (*CanExpr) nodeTag()                           {}
func (*MatchExpr) nodeTag()                         {}
func (*VisitExpr) nodeTag()                         {}
func (*FoldExpr) nodeTag()                          {}
func (*EmitExpr) nodeTag()                          {}
func (*MatchWildcardPattern) nodeTag()              {}
func (*MatchBindPattern) nodeTag()                  {}
func (*MatchStringLiteralPattern) nodeTag()         {}
func (*MatchLiteralPattern) nodeTag()               {}
func (*MatchTuplePattern) nodeTag()                 {}
func (*MatchListPattern) nodeTag()                  {}
func (*MatchRestPattern) nodeTag()                  {}
func (*MatchStructPattern) nodeTag()                {}
func (*MatchVariantPattern) nodeTag()               {}
func (*MoveBindNamePattern) nodeTag()               {}
func (*MoveBindStructPattern) nodeTag()             {}
func (*MoveBindTuplePattern) nodeTag()              {}
func (*MoveBindVariantPattern) nodeTag()            {}
func (*AssignStmt) nodeTag()                        {}
func (*AugAssignStmt) nodeTag()                     {}
func (*AsRefAssignStmt) nodeTag()                   {}
func (*VarDeclStmt) nodeTag()                       {}
func (*LocalParamsStmt) nodeTag()                   {}
func (*LetDestructureStmt) nodeTag()                {}
func (*TupleBindStmt) nodeTag()                     {}
func (*MoveBindStmt) nodeTag()                      {}
func (*DeferStmt) nodeTag()                         {}
func (*ReturnStmt) nodeTag()                        {}
func (*IfStmt) nodeTag()                            {}
func (*WhileStmt) nodeTag()                         {}
func (*ForStmt) nodeTag()                           {}
func (*IterForStmt) nodeTag()                       {}
func (*ParallelForStmt) nodeTag()                   {}
func (*MatchStmt) nodeTag()                         {}
func (*ExpectPatternStmt) nodeTag()                 {}
func (*InStoreStmt) nodeTag()                       {}
func (*CanStmt) nodeTag()                           {}
func (*WithStmt) nodeTag()                          {}
func (*ArgsScopeStmt) nodeTag()                     {}
func (*CascadeStmt) nodeTag()                       {}
func (*ScopeStmt) nodeTag()                         {}
func (*PoolStmt) nodeTag()                          {}
func (*LockStmt) nodeTag()                          {}
func (*PassStmt) nodeTag()                          {}
func (*SignalStmt) nodeTag()                        {}
func (*PanicStmt) nodeTag()                         {}
func (*ExprStmt) nodeTag()                          {}
func (*StaticIfStmt) nodeTag()                      {}
func (*StaticErrorStmt) nodeTag()                   {}
func (*DiscardStmt) nodeTag()                       {}
func (*RegionStmt) nodeTag()                        {}
func (*DestroyStmt) nodeTag()                       {}
func (*MarkStmt) nodeTag()                          {}
func (*CheckpointStmt) nodeTag()                    {}
func (*GroupedCheckpointStmt) nodeTag()             {}
func (*RestoreStmt) nodeTag()                       {}
func (*RestoreCheckpointStmt) nodeTag()             {}
func (*ResetStmt) nodeTag()                         {}
func (*ConstDecl) declTag()                         {}
func (*TokenSetDecl) declTag()                      {}
func (*ConstEnumDecl) declTag()                     {}
func (*ErrorDecl) declTag()                         {}
func (*EffectsDecl) declTag()                       {}
func (*EffectDecl) declTag()                        {}
func (*PermissionDecl) declTag()                    {}
func (*ContextDecl) declTag()                       {}
func (*ParamsDecl) declTag()                        {}
func (*NamespaceDecl) declTag()                     {}
func (*UsingDecl) declTag()                         {}
func (*EnumDecl) declTag()                          {}
func (*TreeDecl) declTag()                          {}
func (*GrammarDecl) declTag()                       {}
func (*GrammarEnvDecl) declTag()                    {}
func (*LexerDecl) declTag()                         {}
func (*AttributeDecl) declTag()                     {}
func (*GlobalDecl) declTag()                        {}
func (*StructDecl) declTag()                        {}
func (*StoreDecl) declTag()                         {}
func (*FuncDecl) declTag()                          {}
func (*ExternFuncDecl) declTag()                    {}
func (*ExternVarDecl) declTag()                     {}
func (*ExternTypeDecl) declTag()                    {}
func (*TypeAliasDecl) declTag()                     {}
func (*ExportTypeDecl) declTag()                    {}
func (*ExportFuncDecl) declTag()                    {}
func (*ExportGlobalDecl) declTag()                  {}
func (*StaticIfDecl) declTag()                      {}
func (*TypeAliasDecl) nodeTag()                     {}
func (*TreeCategoryDecl) treeMemberDeclTag()        {}
func (*TreeBlockDecl) treeMemberDeclTag()           {}
func (*TreeStructDecl) treeMemberDeclTag()          {}
func (*GrammarPassTerm) grammarTermTag()            {}
func (*GrammarTokenTerm) grammarTermTag()           {}
func (*GrammarTokenKindTerm) grammarTermTag()       {}
func (*GrammarCallTerm) grammarTermTag()            {}
func (*GrammarChoiceTerm) grammarTermTag()          {}
func (*GrammarOptionalTerm) grammarTermTag()        {}
func (*GrammarRecoverTerm) grammarTermTag()         {}
func (*GrammarWhenTerm) grammarTermTag()            {}
func (*GrammarMatchTerm) grammarTermTag()           {}
func (*GrammarRequiredTerm) grammarTermTag()        {}
func (*GrammarDelimitedTerm) grammarTermTag()       {}
func (*GrammarSeqTerm) grammarTermTag()             {}
func (*GrammarLookaheadTerm) grammarTermTag()       {}
func (*GrammarExprTerm) grammarTermTag()            {}
func (*GrammarSingletonTerm) grammarTermTag()       {}
func (*GrammarEmptyTerm) grammarTermTag()           {}
func (*GrammarConcatTerm) grammarTermTag()          {}
func (*GrammarGuardTerm) grammarTermTag()           {}
func (*GrammarAttemptTerm) grammarTermTag()         {}
func (*GrammarCutTerm) grammarTermTag()             {}
func (*GrammarListTerm) grammarTermTag()            {}
func (*GrammarRepeatTerm) grammarTermTag()          {}
func (*GrammarFlatRepeatTerm) grammarTermTag()      {}
func (*GrammarWhileTerm) grammarTermTag()           {}
func (*GrammarSeparatedTerm) grammarTermTag()       {}
func (*GrammarSuffixTerm) grammarTermTag()          {}
func (*GrammarPostfixTerm) grammarTermTag()         {}
func (*GrammarPrecedenceTerm) grammarTermTag()      {}
func (*GrammarInfixTableTerm) grammarTermTag()      {}
func (*GrammarTokenSetRefTerm) grammarTermTag()     {}
func (*GrammarFirstTerm) grammarTermTag()           {}
func (*GrammarApplyTerm) grammarTermTag()           {}
func (*GrammarBindTerm) grammarTermTag()            {}
func (*GrammarAssignTerm) grammarTermTag()          {}
func (*GrammarReturnTerm) grammarTermTag()          {}
func (*NamedType) typeExprTag()                     {}
func (*RefType) typeExprTag()                       {}
func (*RefStateLiteralTypeExpr) typeExprTag()       {}
func (*RefStorageLiteralTypeExpr) typeExprTag()     {}
func (*GenericType) typeExprTag()                   {}
func (*AggregateStateTypeExpr) typeExprTag()        {}
func (*StateSetTypeExpr) typeExprTag()              {}
func (*MutableType) typeExprTag()                   {}
func (*TailType) typeExprTag()                      {}
func (*ArrayType) typeExprTag()                     {}
func (*BuiltinTypeExpr) typeExprTag()               {}
func (*FuncTypeExpr) typeExprTag()                  {}
func (*ErrorSetExpr) typeExprTag()                  {}
func (*ErrorUnionTypeExpr) typeExprTag()            {}
func (*OptionalTypeExpr) typeExprTag()              {}
func (*TupleTypeExpr) typeExprTag()                 {}
func (*Ident) exprTag()                             {}
func (*IntLit) exprTag()                            {}
func (*FloatLit) exprTag()                          {}
func (*StringLit) exprTag()                         {}
func (*CharLit) exprTag()                           {}
func (*BoolLit) exprTag()                           {}
func (*NullLit) exprTag()                           {}
func (*ZeroedLit) exprTag()                         {}
func (*ExprBlock) exprTag()                         {}
func (*BinaryExpr) exprTag()                        {}
func (*UnaryExpr) exprTag()                         {}
func (*MoveExpr) exprTag()                          {}
func (*CallExpr) exprTag()                          {}
func (*FieldExpr) exprTag()                         {}
func (*ShorthandMemberExpr) exprTag()               {}
func (*IndexExpr) exprTag()                         {}
func (*SliceExpr) exprTag()                         {}
func (*ListComprehensionExpr) exprTag()             {}
func (*QueryExpr) exprTag()                         {}
func (*MatchWildcardPattern) matchPatternTag()      {}
func (*MatchBindPattern) matchPatternTag()          {}
func (*MatchStringLiteralPattern) matchPatternTag() {}
func (*MatchLiteralPattern) matchPatternTag()       {}
func (*MatchTuplePattern) matchPatternTag()         {}
func (*MatchListPattern) matchPatternTag()          {}
func (*MatchRestPattern) matchPatternTag()          {}
func (*MatchStructPattern) matchPatternTag()        {}
func (*MatchVariantPattern) matchPatternTag()       {}
func (*MoveBindNamePattern) moveBindPatternTag()    {}
func (*MoveBindStructPattern) moveBindPatternTag()  {}
func (*MoveBindTuplePattern) moveBindPatternTag()   {}
func (*MoveBindVariantPattern) moveBindPatternTag() {}
func (*ListLitExpr) exprTag()                       {}
func (*CastExpr) exprTag()                          {}
func (*CascadeExpr) exprTag()                       {}
func (*LambdaExpr) exprTag()                        {}
func (*SizeofExpr) exprTag()                        {}
func (*TernaryExpr) exprTag()                       {}
func (*AddrOfExpr) exprTag()                        {}
func (*SpecializeExpr) exprTag()                    {}
func (*StructLitExpr) exprTag()                     {}
func (*RecordUpdateExpr) exprTag()                  {}
func (*TupleExpr) exprTag()                         {}
func (*VariantTestExpr) exprTag()                   {}
func (*StructTestExpr) exprTag()                    {}
func (*IsPatternExpr) exprTag()                     {}
func (*TypeExprExpr) exprTag()                      {}
func (*ParenExpr) exprTag()                         {}
func (*RaiseExpr) exprTag()                         {}
func (*MatchExpr) exprTag()                         {}
func (*VisitExpr) exprTag()                         {}
func (*FoldExpr) exprTag()                          {}
func (*EmitExpr) exprTag()                          {}
func (*MatchStmt) stmtTag()                         {}
func (*ExpectPatternStmt) stmtTag()                 {}
func (*TryExpr) exprTag()                           {}
func (*CatchExpr) exprTag()                         {}
func (*UnwrapElseExpr) exprTag()                    {}
func (*OptionalBindExpr) exprTag()                  {}
func (*AllocExpr) exprTag()                         {}
func (*CanExpr) exprTag()                           {}
func (*AssignStmt) stmtTag()                        {}
func (*AugAssignStmt) stmtTag()                     {}
func (*AsRefAssignStmt) stmtTag()                   {}
func (*VarDeclStmt) stmtTag()                       {}
func (*LocalParamsStmt) stmtTag()                   {}
func (*LetDestructureStmt) stmtTag()                {}
func (*TupleBindStmt) stmtTag()                     {}
func (*MoveBindStmt) stmtTag()                      {}
func (*DeferStmt) stmtTag()                         {}
func (*ReturnStmt) stmtTag()                        {}
func (*IfStmt) stmtTag()                            {}
func (*WhileStmt) stmtTag()                         {}
func (*ForStmt) stmtTag()                           {}
func (*IterForStmt) stmtTag()                       {}
func (*ParallelForStmt) stmtTag()                   {}
func (*InStoreStmt) stmtTag()                       {}
func (*CanStmt) stmtTag()                           {}
func (*WithStmt) stmtTag()                          {}
func (*ArgsScopeStmt) stmtTag()                     {}
func (*CascadeStmt) stmtTag()                       {}
func (*ScopeStmt) stmtTag()                         {}
func (*PoolStmt) stmtTag()                          {}
func (*LockStmt) stmtTag()                          {}
func (*PassStmt) stmtTag()                          {}
func (*SignalStmt) stmtTag()                        {}
func (*PanicStmt) stmtTag()                         {}
func (*ExprStmt) stmtTag()                          {}
func (*StaticIfStmt) stmtTag()                      {}
func (*StaticErrorStmt) stmtTag()                   {}
func (*DiscardStmt) stmtTag()                       {}
func (*RegionStmt) stmtTag()                        {}
func (*DestroyStmt) stmtTag()                       {}
func (*MarkStmt) stmtTag()                          {}
func (*CheckpointStmt) stmtTag()                    {}
func (*GroupedCheckpointStmt) stmtTag()             {}
func (*RestoreStmt) stmtTag()                       {}
func (*RestoreCheckpointStmt) stmtTag()             {}
func (*ResetStmt) stmtTag()                         {}
func (n *CallExpr) ArgName(index int) string {
	if n == nil || index < 0 || index >= len(n.ArgNames) {
		return ""
	}
	return n.ArgNames[index]
}
func (n *CallExpr) ArgIsShorthand(index int) bool {
	if n == nil || index < 0 || index >= len(n.ArgShorthand) {
		return false
	}
	return n.ArgShorthand[index]
}
func (n *CallExpr) NamedArgCount() int {
	if n == nil {
		return 0
	}
	count := 0
	for _, name := range n.ArgNames {
		if name != "" {
			count++
		}
	}
	return count
}
