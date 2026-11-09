package ast

import "elisacore/src/lexer"

func (n *GrammarReturnTerm) Pos() lexer.Pos {
	return n.Position
}
func (n *GlobalDecl) Pos() lexer.Pos       { return n.Position }
func (n *GlobalRegionDecl) Pos() lexer.Pos { return n.Position }
func (n *StructDecl) Pos() lexer.Pos       { return n.Position }
func (n *StoreDecl) Pos() lexer.Pos        { return n.Position }
func (n *FuncDecl) Pos() lexer.Pos         { return n.Position }
func (n *ExternFuncDecl) Pos() lexer.Pos   { return n.Position }
func (n *ExternVarDecl) Pos() lexer.Pos    { return n.Position }
func (n *ExternTypeDecl) Pos() lexer.Pos   { return n.Position }
func (n *TypeAliasDecl) Pos() lexer.Pos    { return n.Position }
func (n *RefineDecl) Pos() lexer.Pos       { return n.Position }
func (n *ExportTypeDecl) Pos() lexer.Pos   { return n.Position }
func (n *ExportFuncDecl) Pos() lexer.Pos   { return n.Position }
func (n *ExportGlobalDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *StaticIfDecl) Pos() lexer.Pos     { return n.Position }
func (n *StaticAssertDecl) Pos() lexer.Pos { return n.Position }
func (n *StaticAssertBlockDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *StaticGenerateDecl) Pos() lexer.Pos { return n.Position }
func (n *StaticGenerateEmitDecl) Pos() lexer.Pos {
	return n.Position
}
func (n *StaticGenerateForDecl) Pos() lexer.Pos { return n.Position }
func (n *StaticGenerateIfDecl) Pos() lexer.Pos  { return n.Position }
func (n *NamedType) Pos() lexer.Pos             { return n.Position }
func (n *RefType) Pos() lexer.Pos               { return n.Position }
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
func (n *OwnedType) Pos() lexer.Pos        { return n.Position }
func (n *TailType) Pos() lexer.Pos         { return n.Position }
func (n *ArrayType) Pos() lexer.Pos        { return n.Position }
func (n *BuiltinTypeExpr) Pos() lexer.Pos  { return n.Position }
func (n *GenericValueArgTypeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *FuncTypeExpr) Pos() lexer.Pos { return n.Position }
func (n *ErrorSetExpr) Pos() lexer.Pos { return n.Position }
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
func (n *EnumColumnExpr) Pos() lexer.Pos   { return n.Position }
func (n *ShorthandMemberExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *IndexExpr) Pos() lexer.Pos   { return n.Position }
func (n *SliceExpr) Pos() lexer.Pos   { return n.Position }
func (n *ListLitExpr) Pos() lexer.Pos { return n.Position }
func (n *MembershipRangeExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *ListComprehensionExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *QueryExpr) Pos() lexer.Pos      { return n.Position }
func (n *CastExpr) Pos() lexer.Pos       { return n.Position }
func (n *LambdaExpr) Pos() lexer.Pos     { return n.Position }
func (n *SizeofExpr) Pos() lexer.Pos     { return n.Position }
func (n *AlignofExpr) Pos() lexer.Pos    { return n.Position }
func (n *OffsetofExpr) Pos() lexer.Pos   { return n.Position }
func (n *TernaryExpr) Pos() lexer.Pos    { return n.Position }
func (n *AddrOfExpr) Pos() lexer.Pos     { return n.Position }
func (n *QuantifierExpr) Pos() lexer.Pos { return n.Position }
func (n *SpecializeExpr) Pos() lexer.Pos { return n.Position }
func (n *StructLitExpr) Pos() lexer.Pos  { return n.Position }
func (n *RecordUpdateExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *TupleExpr) Pos() lexer.Pos       { return n.Position }
func (n *VariantTestExpr) Pos() lexer.Pos { return n.Position }
func (n *StructTestExpr) Pos() lexer.Pos  { return n.Position }
func (n *IsPatternExpr) Pos() lexer.Pos   { return n.Position }
func (n *IsAliasExpr) Pos() lexer.Pos     { return n.Position }
func (n *TypeExprExpr) Pos() lexer.Pos    { return n.Position }
func (n *ParenExpr) Pos() lexer.Pos       { return n.Position }
func (n *RaiseExpr) Pos() lexer.Pos       { return n.Position }
func (n *TryExpr) Pos() lexer.Pos         { return n.Position }
func (n *GetExpr) Pos() lexer.Pos         { return n.Position }
func (n *CatchExpr) Pos() lexer.Pos       { return n.Position }
func (n *UnwrapElseExpr) Pos() lexer.Pos  { return n.Position }
func (n *OptionalBindExpr) Pos() lexer.Pos {
	return n.Position
}
func (n *AllocExpr) Pos() lexer.Pos       { return n.Position }
func (n *CanExpr) Pos() lexer.Pos         { return n.Position }
func (n *MatchExpr) Pos() lexer.Pos       { return n.Position }
func (n *MachineFromExpr) Pos() lexer.Pos { return n.Position }
func (n *FoldExpr) Pos() lexer.Pos        { return n.Position }
func (n *EmitExpr) Pos() lexer.Pos        { return n.Position }
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
func (n *MatchOrPattern) Pos() lexer.Pos      { return n.Position }
func (n *MatchRestPattern) Pos() lexer.Pos    { return n.Position }
func (n *MatchRangePattern) Pos() lexer.Pos   { return n.Position }
func (n *MatchStructPattern) Pos() lexer.Pos  { return n.Position }
func (n *MatchVariantPattern) Pos() lexer.Pos { return n.Position }
func (n *MoveBindNamePattern) Pos() lexer.Pos { return n.Position }
func (n *MoveBindStructPattern) Pos() lexer.Pos {
	return n.Position
}
func (n *MoveBindTuplePattern) Pos() lexer.Pos   { return n.Position }
func (n *MoveBindVariantPattern) Pos() lexer.Pos { return n.Position }
func (n *AssignStmt) Pos() lexer.Pos             { return n.Position }
func (n *AugAssignStmt) Pos() lexer.Pos          { return n.Position }
func (n *AsRefAssignStmt) Pos() lexer.Pos        { return n.Position }
func (n *VarDeclStmt) Pos() lexer.Pos            { return n.Position }
func (n *LetDestructureStmt) Pos() lexer.Pos     { return n.Position }
func (n *TupleBindStmt) Pos() lexer.Pos          { return n.Position }
func (n *MoveBindStmt) Pos() lexer.Pos           { return n.Position }
func (n *DeferStmt) Pos() lexer.Pos              { return n.Position }
func (n *ReturnStmt) Pos() lexer.Pos             { return n.Position }
func (n *BreakStmt) Pos() lexer.Pos              { return n.Position }
func (n *ContinueStmt) Pos() lexer.Pos           { return n.Position }
func (n *IfStmt) Pos() lexer.Pos                 { return n.Position }
func (n *WhileStmt) Pos() lexer.Pos              { return n.Position }
func (n *ForStmt) Pos() lexer.Pos                { return n.Position }
func (n *IterForStmt) Pos() lexer.Pos            { return n.Position }
func (n *ParallelForStmt) Pos() lexer.Pos        { return n.Position }
func (n *MatchStmt) Pos() lexer.Pos              { return n.Position }
func (n *ExpectPatternStmt) Pos() lexer.Pos      { return n.Position }
func (n *InStoreStmt) Pos() lexer.Pos            { return n.Position }
func (n *CanStmt) Pos() lexer.Pos                { return n.Position }
func (n *PoolStmt) Pos() lexer.Pos               { return n.Position }
func (n *LockStmt) Pos() lexer.Pos               { return n.Position }
func (n *PassStmt) Pos() lexer.Pos               { return n.Position }
func (n *MachineCoverageStmt) Pos() lexer.Pos    { return n.Position }
func (n *SignalStmt) Pos() lexer.Pos             { return n.Position }
func (n *PanicStmt) Pos() lexer.Pos              { return n.Position }
func (n *ExprStmt) Pos() lexer.Pos               { return n.Position }
func (n *StaticIfStmt) Pos() lexer.Pos           { return n.Position }
func (n *StaticErrorStmt) Pos() lexer.Pos        { return n.Position }
func (n *StaticAssertStmt) Pos() lexer.Pos       { return n.Position }
func (n *AssertByStmt) Pos() lexer.Pos           { return n.Position }
func (n *ProofBlockStmt) Pos() lexer.Pos         { return n.Position }
func (n *AssertHoleStmt) Pos() lexer.Pos         { return n.Position }
func (n *ProofUseStmt) Pos() lexer.Pos           { return n.Position }
func (n *ContractStmt) Pos() lexer.Pos           { return n.Position }
func (n *StaticAssertBlockStmt) Pos() lexer.Pos  { return n.Position }
func (n *StaticBlockStmt) Pos() lexer.Pos        { return n.Position }
func (n *DiscardStmt) Pos() lexer.Pos            { return n.Position }
func (n *RegionStmt) Pos() lexer.Pos             { return n.Position }
func (n *DestroyStmt) Pos() lexer.Pos            { return n.Position }
func (n *LeakStmt) Pos() lexer.Pos               { return n.Position }
func (n *MarkStmt) Pos() lexer.Pos               { return n.Position }
func (n *CheckpointStmt) Pos() lexer.Pos         { return n.Position }
func (n *GroupedCheckpointStmt) Pos() lexer.Pos  { return n.Position }
func (n *RestoreStmt) Pos() lexer.Pos            { return n.Position }
func (n *RestoreCheckpointStmt) Pos() lexer.Pos  { return n.Position }
func (n *ResetStmt) Pos() lexer.Pos              { return n.Position }
func (n *ScopeStmt) Pos() lexer.Pos              { return n.Position }
