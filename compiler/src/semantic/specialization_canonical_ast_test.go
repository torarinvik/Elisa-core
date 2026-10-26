//go:build cgo

package semantic

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"elisacore/src/ast"
)

// Embedding preserves the AST interface while exposing a future/unknown AST
// implementation with opaque semantic metadata and inaccessible fields.
type canonicalOpaqueExprProbe struct {
	ast.Expr
	Payload *canonicalOpaqueMetadataProbe
	hidden  *Symbol
}

type canonicalOpaqueMetadataProbe struct {
	Payload any
}

func TestCanonicalTypedASTOpaqueSkipsOnlyAbsentValues(t *testing.T) {
	calls := 0
	visit := func(any) bool { calls++; return false }
	for _, value := range []reflect.Value{
		{},
		reflect.ValueOf(map[string]ast.Expr(nil)),
		reflect.ValueOf([]ast.Expr(nil)),
		reflect.Zero(reflect.TypeFor[any]()),
		reflect.ValueOf((*ast.IntLit)(nil)),
	} {
		canonicalASTWalkOpaque(value, visit, nil)
	}
	if calls != 0 {
		t.Fatal("absent optional AST fields still reached the boxed fallback")
	}
	for _, value := range []reflect.Value{
		reflect.ValueOf(map[string]ast.Expr{}),
		reflect.ValueOf([]ast.Expr{}),
		reflect.ValueOf(&canonicalOpaqueMetadataProbe{}),
	} {
		canonicalASTWalkOpaque(value, visit, nil)
	}
	if calls != 3 {
		t.Fatal("present opaque fields bypassed the complete fallback policy")
	}
}

func TestCanonicalTypedASTWalkPreservesMapKeysOpaqueMetadataAndCycles(t *testing.T) {
	leaf := &ast.IntLit{Value: "5"}
	visibilityOnly := &ast.FuncDecl{Name: "visibility_only", Body: []ast.Stmt{&ast.ReturnStmt{Value: leaf}}}
	scope := NewScope(nil)
	symbol := &Symbol{Name: "opaque", Node: visibilityOnly, Type: &FuncType{Return: &BuiltinType{Name: "i64"}, ExplicitParamDefaultExprs: []ast.Expr{leaf}}}
	unknown := &canonicalOpaqueExprProbe{Expr: leaf, hidden: symbol}
	payload := map[ast.Node]any{unknown: scope, visibilityOnly: symbol}
	payload[leaf] = payload
	// The embedding also promotes ast.Node onto the struct value, so keep
	// that value comparable as the production AST contract requires. The
	// separate metadata object still exercises an opaque cyclic map.
	unknown.Payload = &canonicalOpaqueMetadataProbe{Payload: payload}
	call := &ast.CallExpr{Func: unknown, ResolvedCommonArgs: map[string]ast.Expr{"only_in_map": &ast.IntLit{Value: "19"}}}
	call.ResolvedCommonArgs["cycle"] = call
	file := &ast.File{Decls: []ast.Decl{&ast.FuncDecl{Name: "caller", Body: []ast.Stmt{&ast.ReturnStmt{Value: call}}}}, DeclVisibility: map[ast.Decl]string{visibilityOnly: "public"}}

	current, reference := newCanonicalMembershipIndexTestScan(), newCanonicalMembershipTestScan()
	current.walk(reflect.ValueOf(file), false)
	reference.walkCanonicalReference(reflect.ValueOf(file), false)
	assertCanonicalMembershipIndexesEqual(t, current, reference)
	for _, node := range []ast.Node{leaf, visibilityOnly, unknown, call, call.ResolvedCommonArgs["only_in_map"]} {
		if !current.owned[node] {
			t.Fatalf("typed canonical walk dropped %T", node)
		}
	}
	if !current.rootSymbols[symbol] || !current.rootScopes[scope] {
		t.Fatal("opaque AST metadata lost a semantic identity")
	}
	if len(current.queue) != 0 {
		t.Fatal("canonical typed traversal retained a private publication queue")
	}
}

func TestCanonicalTypedASTCatalogRefreshesAfterRetainedBodyReplacement(t *testing.T) {
	const source = `def changed() -> i64:
    return 1

def main() -> i64:
    return changed()
`
	_, retained := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if retained == nil {
		t.Fatal("baseline was not retained")
	}
	for step, text := range []string{source, strings.Replace(source, "return 1", "return 2", 1)} {
		var root *Result
		if step == 0 {
			root = retained.result
		} else {
			var err error
			root, err = retained.Update(retainedParse(t, text), retainedParse(t, text), []byte(text))
			if err != nil {
				t.Fatal(err)
			}
		}
		reference := canonicalMembershipCatalogTestScan(root, true)
		root.BeginSharedCodegen()
		nodes := canonicalSpecializationNodes(root)
		if !reflect.DeepEqual(nodes, reference.owned) || !reflect.DeepEqual(root.shared.canonicalSymbols, reference.rootSymbols) || !reflect.DeepEqual(root.shared.canonicalScopes, reference.rootScopes) {
			t.Fatalf("membership changed after retained step %d", step)
		}
		root.EndSharedCodegen()
	}
}

func TestCanonicalTypedASTSharedCatalogHasOneConcurrentPublication(t *testing.T) {
	root, _ := privateSpecializationFixture(t)
	reference := canonicalMembershipCatalogTestScan(root, true)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	const callers = 12
	indexes := make([]map[ast.Node]bool, callers)
	var wait sync.WaitGroup
	for i := range indexes {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			indexes[i] = canonicalSpecializationNodes(root)
		}(i)
	}
	wait.Wait()
	identity := reflect.ValueOf(indexes[0]).Pointer()
	for _, nodes := range indexes {
		if reflect.ValueOf(nodes).Pointer() != identity || !reflect.DeepEqual(nodes, reference.owned) {
			t.Fatal("concurrent callers did not receive the same complete immutable catalog")
		}
	}
}
