package parser

import (
	"fmt"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func nestedAutoRegionExprBlocks(depth int) []ast.Stmt {
	var expr ast.Expr = &ast.IntLit{Value: "1"}
	for i := 0; i < depth; i++ {
		expr = &ast.ExprBlock{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	}
	return []ast.Stmt{&ast.ReturnStmt{Value: expr}}
}

func TestParseNestedBareExprBlocksWithoutAutoRegion(t *testing.T) {
	const depth = 16
	var source strings.Builder
	source.WriteString("def nested() -> i64:\n")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&source, "%svalue%d: i64 =\n", strings.Repeat("    ", i+1), i)
	}
	source.WriteString(strings.Repeat("    ", depth+1) + "1\n")
	for i := depth - 1; i > 0; i-- {
		fmt.Fprintf(&source, "%svalue%d\n", strings.Repeat("    ", i+1), i)
	}
	source.WriteString("    return value0\n")
	l := lexer.New("nested.elisa", []byte(source.String()))
	p := New(l.Tokenize())
	file := p.ParseFile("nested.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 || len(p.Notices()) != 0 {
		t.Fatalf("nested bare blocks failed: lexer=%v parser=%v notices=%v", l.Errors(), p.Errors(), p.Notices())
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if len(fn.Body) != 2 {
		t.Fatalf("nonallocating blocks unexpectedly changed function body: %T", fn.Body[0])
	}
	if _, ok := fn.Body[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("nonallocating blocks unexpectedly acquired an auto region: %T", fn.Body[0])
	}
}

func TestAutoRegionNestedExprBlocks(t *testing.T) {
	if functionBodyNeedsAutoRegion(nestedAutoRegionExprBlocks(24)) {
		t.Fatal("nonallocating nested blocks require no region")
	}
	allocating := &ast.ExprBlock{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.AllocExpr{AutoRegion: true}}}}
	for _, block := range []*ast.ExprBlock{
		{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: allocating}}},
		{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}, Value: allocating},
		{Value: allocating},
	} {
		if !exprBlocksNeedAutoRegion([]ast.Stmt{&ast.ReturnStmt{Value: block}}) {
			t.Fatal("nested allocating block lost its auto-region requirement")
		}
	}
}

func nestedMixedAutoRegionBlocks(depth int) []ast.Stmt {
	body := []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}
	for i := 0; i < depth; i++ {
		body = []ast.Stmt{&ast.IfStmt{Then: []ast.Stmt{&ast.ReturnStmt{Value: &ast.ExprBlock{Stmts: body}}}}}
	}
	return body
}

func TestAutoRegionMixedBlocksAndFreshInvocation(t *testing.T) {
	if functionBodyNeedsAutoRegion(nestedMixedAutoRegionBlocks(20)) {
		t.Fatal("nonallocating mixed blocks require no region")
	}
	block := &ast.ExprBlock{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}}
	body := []ast.Stmt{&ast.ReturnStmt{Value: block}}
	if exprBlocksNeedAutoRegion(body) {
		t.Fatal("nonallocating block requires no region")
	}
	block.Stmts = append(block.Stmts, &ast.ReturnStmt{Value: &ast.AllocExpr{AutoRegion: true}})
	if !exprBlocksNeedAutoRegion(body) {
		t.Fatal("a later scan reused stale results after AST mutation")
	}
}

func BenchmarkAutoRegionNestedExprBlocks(b *testing.B) {
	for _, depth := range []int{0, 4, 8, 12} {
		b.Run(fmt.Sprintf("depth%d", depth), func(b *testing.B) {
			body := nestedAutoRegionExprBlocks(depth)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if functionBodyNeedsAutoRegion(body) {
					b.Fatal("nonallocating blocks require no region")
				}
			}
		})
	}
}

func BenchmarkAutoRegionMixedNestedBlocks(b *testing.B) {
	for _, depth := range []int{4, 8, 12} {
		b.Run(fmt.Sprintf("depth%d", depth), func(b *testing.B) {
			body := nestedMixedAutoRegionBlocks(depth)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if functionBodyNeedsAutoRegion(body) {
					b.Fatal("nonallocating blocks require no region")
				}
			}
		})
	}
}

func BenchmarkAutoRegionNestedStatements(b *testing.B) {
	for _, depth := range []int{16, 32, 64} {
		b.Run(fmt.Sprintf("depth%d", depth), func(b *testing.B) {
			body := []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}
			for i := 0; i < depth; i++ {
				body = []ast.Stmt{&ast.IfStmt{Then: body}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if functionBodyNeedsAutoRegion(body) {
					b.Fatal("nonallocating statements require no region")
				}
			}
		})
	}
}
