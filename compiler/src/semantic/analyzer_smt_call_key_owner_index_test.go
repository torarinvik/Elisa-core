//go:build cgo

package semantic

import (
	"strconv"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func BenchmarkCallKeyTopLevelLocalLookup(b *testing.B) {
	const localCount = 512
	owner := &ast.FuncDecl{Name: "owner"}
	owner.Body = make([]ast.Stmt, 0, localCount+1)
	var target *ast.VarDeclStmt
	for i := 0; i < localCount; i++ {
		local := &ast.VarDeclStmt{
			Position: lexer.Pos{Offset: i + 1},
			Name:     "local_" + strconv.Itoa(i),
			Value:    &ast.IntLit{Position: lexer.Pos{Offset: i + 1}, Value: "1"},
		}
		owner.Body = append(owner.Body, local)
		target = local
	}
	identifier := &ast.Ident{Position: lexer.Pos{Offset: localCount + 2}, Name: target.Name}
	owner.Body = append(owner.Body, &ast.ReturnStmt{Value: identifier})
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: target.Name, Kind: SymbolLocal, Node: target})
	analyzer := &Analyzer{currentScope: scope}
	tr := analyzer.newSMTTranslator(nil)
	if tr.callKeyTopLevelLocal(identifier, owner) == nil {
		b.Fatal("benchmark fixture did not resolve its immutable local")
	}

	b.Run("cached_index", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if tr.callKeyTopLevelLocal(identifier, owner) == nil {
				b.Fatal("cached lookup failed")
			}
		}
	})
	b.Run("uncached_ast_scan", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if callKeyTopLevelLocalScanBaseline(tr, identifier, owner) == nil {
				b.Fatal("baseline lookup failed")
			}
		}
	})
}

func callKeyTopLevelLocalScanBaseline(tr *smtTranslator, id *ast.Ident, owner *ast.FuncDecl) *ast.VarDeclStmt {
	if tr == nil || tr.a == nil || id == nil || owner == nil || tr.a.currentScope == nil {
		return nil
	}
	belongsToOwner := false
	tr.a.walkStaticStmts(owner.Body, func(expr ast.Expr) bool {
		if expr == id {
			belongsToOwner = true
			return true
		}
		return false
	})
	if !belongsToOwner {
		return nil
	}
	var declaration *ast.VarDeclStmt
	for _, stmt := range owner.Body {
		local, ok := stmt.(*ast.VarDeclStmt)
		if ok && local.Name == id.Name && local.Position.Offset > 0 && local.Position.Offset < id.Pos().Offset {
			declaration = local
		}
	}
	if declaration == nil || declaration.Mutable || declaration.Value == nil {
		return nil
	}
	sym, ok := tr.a.currentScope.Lookup(id.Name)
	if !ok || sym == nil || sym.Mutable || sym.Kind != SymbolLocal || sym.Node != declaration {
		return nil
	}
	return declaration
}

func TestCallKeyOwnerIndexCachesOnlyOwnedASTIdentifiers(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "call_key_owner_index.elisa", `def first(value: i64) -> i64:
    local: i64 = value + 1
    return identity(local)
def second(local: i64) -> i64:
    return local
def identity(value: i64) -> i64:
    return value
`, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("test source should analyze cleanly: %v", errs)
	}
	var first, second *ast.FuncDecl
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name {
		case "first":
			first = fn
		case "second":
			second = fn
		}
	}
	if first == nil || second == nil {
		t.Fatal("test functions were not found")
	}
	findIdentifier := func(owner *ast.FuncDecl, name string) *ast.Ident {
		var found *ast.Ident
		result.analyzer.walkStaticStmts(owner.Body, func(expr ast.Expr) bool {
			id, ok := expr.(*ast.Ident)
			if ok && id.Name == name {
				found = id
				return true
			}
			return false
		})
		return found
	}
	ownedUse := findIdentifier(first, "local")
	foreignUse := findIdentifier(second, "local")
	if ownedUse == nil || foreignUse == nil {
		t.Fatal("local identifier nodes were not found")
	}

	tr := result.analyzer.newSMTTranslator(nil)
	index := tr.callKeyOwnerIndex(first)
	if index == nil || index != tr.callKeyOwnerIndex(first) {
		t.Fatal("owner index should be cached for the duration of one SMT translation")
	}
	if _, ok := index.identifiers[ownedUse]; !ok {
		t.Fatal("owner index omitted an identifier in the function body")
	}
	if _, ok := index.identifiers[foreignUse]; ok {
		t.Fatal("owner index admitted an identifier owned by another function")
	}
	locals := index.locals["local"]
	if len(locals) != 1 || locals[0].Name != "local" {
		t.Fatalf("owner index should retain only first's top-level local declaration, got %#v", locals)
	}
}

func TestCallKeyTopLevelLocalDeclinesNestedShadow(t *testing.T) {
	result := analyzeContractStrict(t, "call_key_nested_shadow.elisa", `ghost def recurse(n: i64, seed: i64) -> i64:
    requires n >= 0
    decreases n
    value: i64 = seed
    if n == 0:
        return value
    else:
        value: i64 = seed + 1
        return recurse(n - 1, value)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("test source should analyze cleanly: %v", errs)
	}
	owner := result.File.Decls[0].(*ast.FuncDecl)
	if len(owner.Body) < 2 {
		t.Fatal("expected a top-level local followed by an if statement")
	}
	outer, ok := owner.Body[0].(*ast.VarDeclStmt)
	if !ok || outer.Name != "value" {
		t.Fatal("top-level value declaration was not found")
	}
	branch, ok := owner.Body[1].(*ast.IfStmt)
	if !ok || len(branch.Else) == 0 {
		t.Fatal("nested shadow branch was not found")
	}
	var nested *ast.VarDeclStmt
	var nestedUse *ast.Ident
	for _, stmt := range branch.Else {
		if local, ok := stmt.(*ast.VarDeclStmt); ok && local.Name == "value" {
			nested = local
		}
	}
	result.analyzer.walkStaticStmts(owner.Body, func(expr ast.Expr) bool {
		if node, ok := expr.(*ast.CallExpr); ok {
			if callee, ok := node.Func.(*ast.Ident); ok && callee.Name == "recurse" && len(node.Args) == 2 {
				nestedUse, _ = node.Args[1].(*ast.Ident)
			}
		}
		return false
	})
	if outer == nil || nested == nil || nestedUse == nil {
		t.Fatal("outer local, nested shadow, or recursive call argument was not found")
	}

	outerScope := NewScope(result.GlobalScope)
	outerScope.Define(&Symbol{Name: "value", Kind: SymbolLocal, Node: outer})
	innerScope := NewScope(outerScope)
	innerScope.Define(&Symbol{Name: "value", Kind: SymbolLocal, Node: nested})
	result.analyzer.currentScope = innerScope
	tr := result.analyzer.newSMTTranslator(nil)
	if local := tr.callKeyTopLevelLocal(nestedUse, owner); local != nil {
		t.Fatalf("nested shadow must not canonicalize to top-level local at %d", local.Position.Offset)
	}
}
