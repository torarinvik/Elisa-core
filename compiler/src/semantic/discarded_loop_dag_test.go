//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestDiscardedLoopWalkHandlesSharedResolvedArguments(t *testing.T) {
	var expr ast.Expr = &ast.BoolLit{Value: true}
	for i := 0; i < 128; i++ {
		expr = &ast.CallExpr{Func: &ast.Ident{Name: "ident"}, Args: []ast.Expr{expr}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{expr}}
	}
	analyzer := &Analyzer{}
	analyzer.checkDiscardedLoopValues(&ast.FuncDecl{Body: []ast.Stmt{&ast.ExprStmt{Expr: expr}}})
	if len(analyzer.diagnostics) != 0 {
		t.Fatalf("shared call DAG produced diagnostics: %+v", analyzer.diagnostics)
	}
}

func TestDiscardedLoopWalkReportsSharedBlockOnce(t *testing.T) {
	loop := &ast.ExprBlock{Stmts: []ast.Stmt{&ast.VarDeclStmt{Name: "valid"}, &ast.ForStmt{}}, Value: &ast.Ident{Name: "valid"}}
	block := &ast.ExprBlock{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: loop}}, Value: &ast.BoolLit{Value: true}}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "ident"}, Args: []ast.Expr{block}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{block}}
	analyzer := &Analyzer{}
	analyzer.checkDiscardedLoopValues(&ast.FuncDecl{Body: []ast.Stmt{&ast.ExprStmt{Expr: call}}})
	if len(analyzer.diagnostics) != 1 {
		t.Fatalf("discarded source loop diagnostics=%d, want1: %+v", len(analyzer.diagnostics), analyzer.diagnostics)
	}
}

func TestRegionWalksHandleSharedResolvedArguments(t *testing.T) {
	for _, hasRegion := range []bool{false, true} {
		var expr ast.Expr = &ast.BoolLit{Value: true}
		if hasRegion {
			expr = &ast.ExprBlock{Stmts: []ast.Stmt{&ast.RegionStmt{}}, Value: expr}
		}
		for i := 0; i < 128; i++ {
			expr = &ast.CallExpr{Func: &ast.Ident{Name: "ident"}, Args: []ast.Expr{expr}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{expr}}
		}
		fn := &ast.FuncDecl{Body: []ast.Stmt{&ast.ExprStmt{Expr: expr}}}
		analyzer := &Analyzer{}
		if got := analyzer.funcOwnsRegion(fn); got != hasRegion {
			t.Fatalf("region ownership=%v,want%v", got, hasRegion)
		}
		if got := analyzer.collectRegionBackedPackedEnums([]*ast.FuncDecl{fn}); len(got) != 0 {
			t.Fatalf("region DAG fabricated packed allocation: %+v", got)
		}
	}
}

func TestSynthesizedRegionWalkHandlesSharedResolvedArguments(t *testing.T) {
	first := &ast.RegionStmt{Name: "__auto_first", Lazy: true}
	second := &ast.RegionStmt{Name: "__auto_second", Lazy: true}
	var expr ast.Expr = &ast.ExprBlock{Stmts: []ast.Stmt{first, second, &ast.RegionStmt{Name: "user", Lazy: true}, &ast.RegionStmt{Name: "__auto_explicit"}}, Value: &ast.BoolLit{Value: true}}
	for i := 0; i < 128; i++ {
		expr = &ast.CallExpr{Func: &ast.Ident{Name: "ident"}, Args: []ast.Expr{expr}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{expr}}
	}
	got := collectSynthesizedRegions([]ast.Stmt{&ast.ExprStmt{Expr: expr}})
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("synthesized regions=%v, want two distinct lazy auto regions in source order", got)
	}
}

func TestBarePackedVariantWalkHandlesSharedResolvedArguments(t *testing.T) {
	for _, payload := range []bool{false, true} {
		variant := &EnumVariant{Name: "Leaf"}
		if payload {
			variant.Payload = []Type{&BuiltinType{Name: "i64"}}
		}
		enum := &EnumType{Name: "Tree", Packed: true, VariantMap: map[string]*EnumVariant{"Leaf": variant}}
		analyzer := &Analyzer{namedTypes: map[string]Type{"Tree": enum}}
		var expr ast.Expr = &ast.FieldExpr{Object: &ast.Ident{Name: "Tree"}, Field: "Leaf"}
		for i := 0; i < 128; i++ {
			expr = &ast.CallExpr{Func: &ast.Ident{Name: "ident"}, Args: []ast.Expr{expr}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{expr}}
		}
		got := analyzer.funcBarePackedVariantRoots(&ast.FuncDecl{Body: []ast.Stmt{&ast.ExprStmt{Expr: expr}}})
		if got["Tree"] != !payload || len(got) != map[bool]int{false: 1, true: 0}[payload] {
			t.Fatalf("payload=%v roots=%v", payload, got)
		}
	}
}
