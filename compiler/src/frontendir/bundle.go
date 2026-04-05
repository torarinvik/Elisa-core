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
		gob.Register(&ast.ConstEnumDecl{})
		gob.Register(&ast.ErrorDecl{})
		gob.Register(&ast.PermissionDecl{})
		gob.Register(&ast.NamespaceDecl{})
		gob.Register(&ast.UsingDecl{})
		gob.Register(&ast.EnumDecl{})
		gob.Register(&ast.GlobalDecl{})
		gob.Register(&ast.StructDecl{})
		gob.Register(&ast.FuncDecl{})
		gob.Register(&ast.ExternFuncDecl{})
		gob.Register(&ast.ExternVarDecl{})
		gob.Register(&ast.ExternTypeDecl{})
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

		// Expressions.
		gob.Register(&ast.Ident{})
		gob.Register(&ast.IntLit{})
		gob.Register(&ast.FloatLit{})
		gob.Register(&ast.StringLit{})
		gob.Register(&ast.CharLit{})
		gob.Register(&ast.BoolLit{})
		gob.Register(&ast.NullLit{})
		gob.Register(&ast.ZeroedLit{})
		gob.Register(&ast.BinaryExpr{})
		gob.Register(&ast.UnaryExpr{})
		gob.Register(&ast.MoveExpr{})
		gob.Register(&ast.CallExpr{})
		gob.Register(&ast.FieldExpr{})
		gob.Register(&ast.ShorthandMemberExpr{})
		gob.Register(&ast.IndexExpr{})
		gob.Register(&ast.SliceExpr{})
		gob.Register(&ast.ListLitExpr{})
		gob.Register(&ast.CastExpr{})
		gob.Register(&ast.SizeofExpr{})
		gob.Register(&ast.TernaryExpr{})
		gob.Register(&ast.AddrOfExpr{})
		gob.Register(&ast.SpecializeExpr{})
		gob.Register(&ast.StructLitExpr{})
		gob.Register(&ast.VariantTestExpr{})
		gob.Register(&ast.ParenExpr{})
		gob.Register(&ast.RaiseExpr{})
		gob.Register(&ast.TryExpr{})
		gob.Register(&ast.UnwrapElseExpr{})
		gob.Register(&ast.AllocExpr{})
		gob.Register(&ast.CanExpr{})
		gob.Register(&ast.MatchExpr{})
		gob.Register(&ast.VisitExpr{})
		gob.Register(&ast.FoldExpr{})

		// Patterns.
		gob.Register(&ast.MatchWildcardPattern{})
		gob.Register(&ast.MatchBindPattern{})
		gob.Register(&ast.MatchStringLiteralPattern{})
		gob.Register(&ast.MatchLiteralPattern{})
		gob.Register(&ast.MatchVariantPattern{})
		gob.Register(&ast.MoveBindNamePattern{})
		gob.Register(&ast.MoveBindStructPattern{})
		gob.Register(&ast.MoveBindVariantPattern{})
		gob.Register(&ast.ViewBindPattern{})

		// Statements.
		gob.Register(&ast.AssignStmt{})
		gob.Register(&ast.AugAssignStmt{})
		gob.Register(&ast.AsRefAssignStmt{})
		gob.Register(&ast.VarDeclStmt{})
		gob.Register(&ast.MoveBindStmt{})
		gob.Register(&ast.OpenStmt{})
		gob.Register(&ast.ViewStmt{})
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
		gob.Register(&ast.PoolStmt{})
		gob.Register(&ast.LockStmt{})
		gob.Register(&ast.PassStmt{})
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
