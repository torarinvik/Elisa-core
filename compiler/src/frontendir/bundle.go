package frontendir

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"sync"

	"llcontext/src/ast"
)

const BundleVersion = 1

type Bundle struct {
	Version        int
	SourceFilename string
	ResolvedSource []byte
	File           *ast.File
}

var registerBundleTypesOnce sync.Once

func Encode(bundle *Bundle) ([]byte, error) {
	registerBundleTypes()
	if bundle == nil {
		return nil, fmt.Errorf("frontend IR bundle is nil")
	}
	copyBundle := *bundle
	if copyBundle.Version == 0 {
		copyBundle.Version = BundleVersion
	}
	var out bytes.Buffer
	if err := gob.NewEncoder(&out).Encode(&copyBundle); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func Decode(data []byte) (*Bundle, error) {
	registerBundleTypes()
	var bundle Bundle
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&bundle); err != nil {
		return nil, err
	}
	if bundle.Version != BundleVersion {
		return nil, fmt.Errorf("unsupported frontend IR version %d", bundle.Version)
	}
	if bundle.File == nil {
		return nil, fmt.Errorf("frontend IR bundle is missing AST")
	}
	return &bundle, nil
}

func registerBundleTypes() {
	registerBundleTypesOnce.Do(func() {
		gob.Register(&Bundle{})
		gob.Register(&ast.File{})

		// Decls.
		gob.Register(&ast.ConstDecl{})
		gob.Register(&ast.TokenSetDecl{})
		gob.Register(&ast.ConstEnumDecl{})
		gob.Register(&ast.ErrorDecl{})
		gob.Register(&ast.EffectsDecl{})
		gob.Register(&ast.EffectDecl{})
		gob.Register(&ast.PermissionDecl{})
		gob.Register(&ast.ContextDecl{})
		gob.Register(&ast.ParamsDecl{})
		gob.Register(&ast.NamespaceDecl{})
		gob.Register(&ast.UsingDecl{})
		gob.Register(&ast.EnumDecl{})
		gob.Register(&ast.GrammarDecl{})
		gob.Register(&ast.GrammarEnvDecl{})
		gob.Register(&ast.LexerDecl{})
		gob.Register(&ast.GlobalDecl{})
		gob.Register(&ast.StructDecl{})
		gob.Register(&ast.InterfaceDecl{})
		gob.Register(&ast.ImplDecl{})
		gob.Register(&ast.AssociatedTypeDecl{})
		gob.Register(&ast.ImplAssociatedTypeDecl{})
		gob.Register(&ast.FuncDecl{})
		gob.Register(&ast.ExternFuncDecl{})
		gob.Register(&ast.ExternVarDecl{})
		gob.Register(&ast.ExternTypeDecl{})
		gob.Register(&ast.TypeAliasDecl{})
		gob.Register(&ast.ExportTypeDecl{})
		gob.Register(&ast.ExportFuncDecl{})
		gob.Register(&ast.ExportGlobalDecl{})
		gob.Register(&ast.StaticIfDecl{})

		// Type expressions.
		gob.Register(&ast.NamedType{})
		gob.Register(&ast.RefType{})
		gob.Register(&ast.RefStateLiteralTypeExpr{})
		gob.Register(&ast.RefStorageLiteralTypeExpr{})
		gob.Register(&ast.GenericType{})
		gob.Register(&ast.AggregateStateTypeExpr{})
		gob.Register(&ast.MutableType{})
		gob.Register(&ast.TailType{})
		gob.Register(&ast.ArrayType{})
		gob.Register(&ast.BuiltinTypeExpr{})
		gob.Register(&ast.FuncTypeExpr{})
		gob.Register(&ast.ErrorSetExpr{})
		gob.Register(&ast.ErrorUnionTypeExpr{})
		gob.Register(&ast.OptionalTypeExpr{})
		gob.Register(&ast.TupleTypeExpr{})

		// Expressions.
		gob.Register(&ast.Ident{})
		gob.Register(&ast.IntLit{})
		gob.Register(&ast.FloatLit{})
		gob.Register(&ast.StringLit{})
		gob.Register(&ast.CharLit{})
		gob.Register(&ast.BoolLit{})
		gob.Register(&ast.NullLit{})
		gob.Register(&ast.ZeroedLit{})
		gob.Register(&ast.ExprBlock{})
		gob.Register(&ast.BinaryExpr{})
		gob.Register(&ast.UnaryExpr{})
		gob.Register(&ast.MoveExpr{})
		gob.Register(&ast.CallExpr{})
		gob.Register(&ast.FieldExpr{})
		gob.Register(&ast.ShorthandMemberExpr{})
		gob.Register(&ast.IndexExpr{})
		gob.Register(&ast.SliceExpr{})
		gob.Register(&ast.ListLitExpr{})
		gob.Register(&ast.ListComprehensionExpr{})
		gob.Register(&ast.CastExpr{})
		gob.Register(&ast.SizeofExpr{})
		gob.Register(&ast.TernaryExpr{})
		gob.Register(&ast.AddrOfExpr{})
		gob.Register(&ast.SpecializeExpr{})
		gob.Register(&ast.StructLitExpr{})
		gob.Register(&ast.RecordUpdateExpr{})
		gob.Register(&ast.TupleExpr{})
		gob.Register(&ast.VariantTestExpr{})
		gob.Register(&ast.StructTestExpr{})
		gob.Register(&ast.IsPatternExpr{})
		gob.Register(&ast.ParenExpr{})
		gob.Register(&ast.RaiseExpr{})
		gob.Register(&ast.TryExpr{})
		gob.Register(&ast.CatchExpr{})
		gob.Register(&ast.UnwrapElseExpr{})
		gob.Register(&ast.AllocExpr{})
		gob.Register(&ast.CanExpr{})
		gob.Register(&ast.MatchExpr{})
		gob.Register(&ast.VisitExpr{})
		gob.Register(&ast.FoldExpr{})

		// Grammar terms.
		gob.Register(&ast.GrammarPassTerm{})
		gob.Register(&ast.GrammarTokenTerm{})
		gob.Register(&ast.GrammarTokenKindTerm{})
		gob.Register(&ast.GrammarCallTerm{})
		gob.Register(&ast.GrammarChoiceTerm{})
		gob.Register(&ast.GrammarOptionalTerm{})
		gob.Register(&ast.GrammarWhenTerm{})
		gob.Register(&ast.GrammarMatchTerm{})
		gob.Register(&ast.GrammarRecoverTerm{})
		gob.Register(&ast.GrammarRequiredTerm{})
		gob.Register(&ast.GrammarDelimitedTerm{})
		gob.Register(&ast.GrammarSeqTerm{})
		gob.Register(&ast.GrammarLookaheadTerm{})
		gob.Register(&ast.GrammarExprTerm{})
		gob.Register(&ast.GrammarSingletonTerm{})
		gob.Register(&ast.GrammarConcatTerm{})
		gob.Register(&ast.GrammarListTerm{})
		gob.Register(&ast.GrammarRepeatTerm{})
		gob.Register(&ast.GrammarFlatRepeatTerm{})
		gob.Register(&ast.GrammarWhileTerm{})
		gob.Register(&ast.GrammarSeparatedTerm{})
		gob.Register(&ast.GrammarSuffixTerm{})
		gob.Register(&ast.GrammarPrecedenceTerm{})
		gob.Register(&ast.GrammarInfixTableTerm{})
		gob.Register(&ast.GrammarTokenSetRefTerm{})
		gob.Register(&ast.GrammarFirstTerm{})
		gob.Register(&ast.GrammarApplyTerm{})
		gob.Register(&ast.GrammarBindTerm{})
		gob.Register(&ast.GrammarAssignTerm{})
		gob.Register(&ast.GrammarReturnTerm{})

		// Patterns.
		gob.Register(&ast.MatchWildcardPattern{})
		gob.Register(&ast.MatchBindPattern{})
		gob.Register(&ast.MatchStringLiteralPattern{})
		gob.Register(&ast.MatchLiteralPattern{})
		gob.Register(&ast.MatchTuplePattern{})
		gob.Register(&ast.MatchListPattern{})
		gob.Register(&ast.MatchRestPattern{})
		gob.Register(&ast.MatchStructPattern{})
		gob.Register(&ast.MatchVariantPattern{})
		gob.Register(&ast.ExpectPatternStmt{})
		gob.Register(&ast.MoveBindNamePattern{})
		gob.Register(&ast.MoveBindStructPattern{})
		gob.Register(&ast.MoveBindTuplePattern{})
		gob.Register(&ast.MoveBindVariantPattern{})

		// Statements.
		gob.Register(&ast.AssignStmt{})
		gob.Register(&ast.AugAssignStmt{})
		gob.Register(&ast.AsRefAssignStmt{})
		gob.Register(&ast.VarDeclStmt{})
		gob.Register(&ast.LetDestructureStmt{})
		gob.Register(&ast.TupleBindStmt{})
		gob.Register(&ast.MoveBindStmt{})
		gob.Register(&ast.DeferStmt{})
		gob.Register(&ast.ReturnStmt{})
		gob.Register(&ast.IfStmt{})
		gob.Register(&ast.WhileStmt{})
		gob.Register(&ast.ForStmt{})
		gob.Register(&ast.IterForStmt{})
		gob.Register(&ast.ParallelForStmt{})
		gob.Register(&ast.MatchStmt{})
		gob.Register(&ast.InStoreStmt{})
		gob.Register(&ast.CanStmt{})
		gob.Register(&ast.WithStmt{})
		gob.Register(&ast.ArgsScopeStmt{})
		gob.Register(&ast.CascadeStmt{})
		gob.Register(&ast.PoolStmt{})
		gob.Register(&ast.LockStmt{})
		gob.Register(&ast.PassStmt{})
		gob.Register(&ast.SignalStmt{})
		gob.Register(&ast.PanicStmt{})
		gob.Register(&ast.ExprStmt{})
		gob.Register(&ast.StaticIfStmt{})
		gob.Register(&ast.StaticErrorStmt{})
		gob.Register(&ast.DiscardStmt{})
		gob.Register(&ast.RegionStmt{})
		gob.Register(&ast.DestroyStmt{})
		gob.Register(&ast.MarkStmt{})
		gob.Register(&ast.RestoreStmt{})
		gob.Register(&ast.ResetStmt{})
	})
}
