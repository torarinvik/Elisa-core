//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestParamStoreTargetsRespectLocalScopes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prefix     []ast.Stmt
		wantGlobal bool
	}{
		{"global-control", nil, true},
		{"function-local-control", []ast.Stmt{&ast.VarDeclStmt{Name: "sink"}}, false},
		{"branch-local-shadow", []ast.Stmt{&ast.IfStmt{Then: []ast.Stmt{&ast.VarDeclStmt{Name: "sink"}}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := &ast.FuncDecl{Name: "stash", Params: []ast.ParamDecl{{Name: "value"}}}
			fn.Body = append(tc.prefix, &ast.AssignStmt{Target: &ast.Ident{Name: "sink"}, Value: &ast.Ident{Name: "value"}})
			a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: {Type: &FuncType{Params: []Type{&SViewType{}}}}}}
			got := a.computeParamStoreTargets([]*ast.FuncDecl{fn})[fn][0][storeTargetGlobal]
			if got != tc.wantGlobal {
				t.Fatalf("global target=%v want %v", got, tc.wantGlobal)
			}
		})
	}
}

func TestParamStoreTargetsRejectGlobalEscapeAfterNestedShadow(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		name := "global-control"
		local := ""
		if shadow {
			name = "branch-local-shadow"
			local = "    if true:\n        sink: i64 = 0\n"
		}
		t.Run(name, func(t *testing.T) {
			source := `struct Snapshot:
    names: mutable darray[i64] = []

global mutable sink: Snapshot = zeroed

def stash(alt: Snapshot) -> void:
` + local + `    can Global.Write:
        sink <- alt

def run() -> void:
    can Memory.Allocate, Abort.Panic, Global.Write:
        region scratch:
            tmp: mutable Snapshot = Snapshot{}
            tmp.names.push(1)
            stash(tmp)
`
			result := analyzeTreeTestSourceWithSemanticErrors(t, "scope-store.elisa", source)
			errors := strings.Join(result.Errors(), "\n")
			t.Log(errors)
			if !strings.Contains(errors, "stored by the callee into program-lifetime storage") {
				t.Fatal("short-lived argument store was not rejected at the call")
			}
		})
	}
}

func TestParamStoreTargetsAllowInScopeLocalSink(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "local-store.elisa", `struct Snapshot:
    names: mutable darray[i64] = []

def stash(alt: Snapshot) -> void:
    sink: mutable Snapshot = zeroed
    sink <- alt

def run() -> void:
    can Memory.Allocate, Abort.Panic:
        region scratch:
            tmp: mutable Snapshot = Snapshot{}
            tmp.names.push(1)
            stash(tmp)
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("a callee-local sink must not become a global escape: %v", errors)
	}
}

func TestParamStoreTargetsMutableIteratorShadowsParameter(t *testing.T) {
	fn := &ast.FuncDecl{
		Name:   "stash",
		Params: []ast.ParamDecl{{Name: "sink"}, {Name: "value"}},
		Body: []ast.Stmt{&ast.IterForStmt{
			Mode:    ast.IterBindMutableRef,
			Pattern: &ast.MoveBindNamePattern{Name: "sink"},
			Source:  &ast.Ident{Name: "globalBuckets"},
			Body:    []ast.Stmt{&ast.AssignStmt{Target: &ast.Ident{Name: "sink"}, Value: &ast.Ident{Name: "value"}}},
		}},
	}
	a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: {Type: &FuncType{Params: []Type{&SViewType{}, &SViewType{}}}}}}
	targets := a.computeParamStoreTargets([]*ast.FuncDecl{fn})[fn][1]
	if !targets[storeTargetGlobal] || targets[0] {
		t.Fatalf("iterator alias must not target the shadowed parameter: %v", targets)
	}
	fn.Body[0].(*ast.IterForStmt).Mode = ast.IterBindValue
	targets = a.computeParamStoreTargets([]*ast.FuncDecl{fn})[fn][1]
	if len(targets) != 0 {
		t.Fatalf("rebinding a by-value loop local must not escape: %v", targets)
	}
}

func TestParamStoreTargetsSourceShadowKeepsParameterOrigins(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "source-shadow-store.elisa", `struct Snapshot:
    names: mutable darray[i64] = []

global mutable sink: Snapshot = zeroed

def stash(alt: Snapshot) -> void:
    can Global.Write:
        if true:
            alt: Snapshot = alt
            sink <- alt

def run() -> void:
    can Memory.Allocate, Abort.Panic, Global.Write:
        region scratch:
            tmp: mutable Snapshot = Snapshot{}
            tmp.names.push(1)
            stash(tmp)
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "stored by the callee into program-lifetime storage") {
		t.Fatalf("shadow-copy of a parameter must retain its source: %v", result.Errors())
	}
}

func TestParamStoreTargetDeclarationOrderAndExprBlockTail(t *testing.T) {
	store := func() ast.Stmt {
		return &ast.AssignStmt{Target: &ast.Ident{Name: "sink"}, Value: &ast.Ident{Name: "value"}}
	}
	for _, tc := range []struct {
		name       string
		body       []ast.Stmt
		wantGlobal bool
	}{
		{"store-before-local", []ast.Stmt{store(), &ast.VarDeclStmt{Name: "sink"}}, true},
		{"nested-in-scope-local", []ast.Stmt{&ast.VarDeclStmt{Name: "sink"}, &ast.IfStmt{Then: []ast.Stmt{store()}}}, false},
		{"expression-block-tail-local", []ast.Stmt{&ast.ReturnStmt{Value: &ast.ExprBlock{Stmts: []ast.Stmt{&ast.VarDeclStmt{Name: "sink"}}, Value: &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "sink"}, Field: "push"}, Args: []ast.Expr{&ast.Ident{Name: "value"}}}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := &ast.FuncDecl{Name: "stash", Params: []ast.ParamDecl{{Name: "value"}}, Body: tc.body}
			a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: {Type: &FuncType{Params: []Type{&SViewType{}}}}}}
			got := a.computeParamStoreTargets([]*ast.FuncDecl{fn})[fn][0][storeTargetGlobal]
			if got != tc.wantGlobal {
				t.Fatalf("global target=%v want %v", got, tc.wantGlobal)
			}
		})
	}
}
