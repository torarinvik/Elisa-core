//go:build cgo

package semantic

import (
	"crypto/sha256"
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func targetedMembershipFixture(t *testing.T, root *Result, fn *ast.FuncDecl) (*specializationContext, *ast.FuncDecl) {
	t.Helper()
	worker := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	isolateWorkerScopes(root.analyzer, worker)
	ctx := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: ctx}
	private := ctx.copyDecl(fn)
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	return ctx, private
}

func TestTargetedMembershipRealSpecializationAvoidsWholeCatalog(t *testing.T) {
	root, fn := privateSpecializationFixture(t)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	product, err := root.SpecializeFunction(fn, []Type{root.NamedTypes["Record"]})
	if err != nil {
		t.Fatal(err)
	}
	if product == nil || product.Decl == fn {
		t.Fatal("missing real private specialization")
	}
	if root.shared.canonicalNodes != nil || root.shared.canonicalSymbols != nil || root.shared.canonicalScopes != nil {
		t.Fatal("fully witnessed specialization built whole root catalog")
	}
}

func TestTargetedMembershipUnknownOpaqueStructuralScopeSymbolAndPristineFallback(t *testing.T) {
	root, fn := privateSpecializationFixture(t)
	detached := &ast.IntLit{Value: "912"}
	privateScope := NewScope(nil)
	alias := &Symbol{Name: "detached-alias", Node: &ast.IntLit{Value: "7"}}
	symbol := &Symbol{Name: "metadata-only", Node: &ast.IntLit{Value: "8"}, Type: &FuncType{ExplicitParamDefaultExprs: []ast.Expr{detached}}, AliasOf: alias}
	opaque := &canonicalOpaqueExprProbe{Expr: &ast.IntLit{Value: "9"}, Payload: &canonicalOpaqueMetadataProbe{Payload: map[ast.Node]any{detached: privateScope}}, hidden: symbol}
	hidden := &ast.FuncDecl{Name: "visibility-only", Body: []ast.Stmt{&ast.ReturnStmt{Value: opaque}}}
	if root.File.DeclVisibility == nil {
		root.File.DeclVisibility = map[ast.Decl]string{}
	}
	root.File.DeclVisibility[hidden] = "private"
	structuralCarrier := &ast.IntLit{Value: "11"}
	structuralDefault := &ast.IntLit{Value: "12"}
	root.ExprTypes[structuralCarrier] = &FuncType{ExplicitParamDefaultExprs: []ast.Expr{structuralDefault}, Return: root.NamedTypes["i64"]}
	reference := canonicalMembershipCatalogTestScan(root, true)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	ctx, private := targetedMembershipFixture(t, root, fn)
	p := newSpecializationPublication(ctx, private)
	if p.err != nil {
		t.Fatal(p.err)
	}
	if root.shared.canonicalNodes != nil {
		t.Fatal("fixture unexpectedly built catalog before unknown query")
	}
	for node, answer := range p.rootNodes {
		if reference.owned[node] != answer {
			t.Fatalf("initial tri-state prediction differs for %T", node)
		}
	}
	for _, node := range []ast.Node{opaque, detached, hidden, structuralCarrier, structuralDefault, alias.Node, symbol.Node} {
		if _, known := p.rootNodes[node]; known {
			t.Fatalf("unknown fixture already witnessed %T", node)
		}
		if got := p.canonicalNodeMember(node); got != reference.owned[node] || !got {
			t.Fatalf("unknown canonical syntax differs for %T", node)
		}
	}
	if root.shared.canonicalNodes == nil {
		t.Fatal("unknown syntax bypassed mandatory complete fallback")
	}
	structuralPublication := newSpecializationPublication(ctx, private)
	structuralPublication.walk(reflect.ValueOf(&FuncType{ExplicitParamDefaultExprs: []ast.Expr{structuralDefault}}), true)
	if structuralPublication.err != nil || structuralPublication.owned[structuralDefault] {
		t.Fatal("structural metadata borrowed canonical AST as private")
	}
	for _, candidate := range []*Symbol{symbol, alias, {Name: "new-private"}} {
		if got := p.canonicalSymbolMember(candidate); got != reference.rootSymbols[candidate] {
			t.Fatal("unknown symbol differs from complete oracle")
		}
	}
	for _, candidate := range []*Scope{privateScope, NewScope(nil)} {
		if got := p.canonicalScopeMember(candidate); got != reference.rootScopes[candidate] {
			t.Fatal("unknown scope differs from complete oracle")
		}
	}
	// A pristine/copied source pointer with no live root edge must not become
	// canonical merely because it is syntax or occupies an old source position.
	pristine := copyASTDecl(fn).(*ast.FuncDecl)
	for _, node := range []ast.Node{pristine, pristine.Body[0], &ast.IndexExpr{}} {
		if got := p.canonicalNodeMember(node); got != reference.owned[node] {
			t.Fatal("unwitnessed pristine/fresh membership differs")
		}
	}
	p.err = nil
	p.walk(reflect.ValueOf(NewScope(nil)), false)
	if p.err == nil || !strings.Contains(p.err.Error(), "private semantic scope") {
		t.Fatal("unknown private scope was accepted")
	}
}

func TestTargetedMembershipRejectsCanonicalDetachedProofAuthority(t *testing.T) {
	root, fn := privateSpecializationFixture(t)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	proof := &ast.IndexExpr{}
	if root.analyzer.indexBoundsProofOwners == nil {
		root.analyzer.indexBoundsProofOwners = map[*ast.IndexExpr]ast.Node{}
	}
	root.analyzer.indexBoundsProofOwners[proof] = fn
	ctx, private := targetedMembershipFixture(t, root, fn)
	// Deliberately install a canonical pointer as current proof authority. Unlike
	// copied fresh nodes this key has no allocation certificate and must fallback.
	ctx.worker.indexBoundsProofOwners = map[*ast.IndexExpr]ast.Node{proof: fn}
	p := newSpecializationPublication(ctx, private)
	if p.err == nil || !strings.Contains(p.err.Error(), "canonical syntax in current proof authority") {
		t.Fatal("canonical detached proof was accepted")
	}
}

func TestTargetedMembershipSerialRootMutationDoesNotReusePartialAnswers(t *testing.T) {
	root, fn := privateSpecializationFixture(t)
	unknown := &ast.IntLit{Value: "between-publications"}
	firstCtx, firstPrivate := targetedMembershipFixture(t, root, fn)
	first := newSpecializationPublication(firstCtx, firstPrivate)
	reference := canonicalMembershipCatalogTestScan(root, true)
	if !reflect.DeepEqual(first.rootNodes, reference.owned) || !reflect.DeepEqual(first.rootSymbols, reference.rootSymbols) || !reflect.DeepEqual(first.rootScopes, reference.rootScopes) {
		t.Fatal("serial constructor no longer uses historical complete catalogs")
	}
	initialSize := len(first.rootNodes)

	if first.canonicalNodeMember(unknown) {
		t.Fatal("unknown node unexpectedly canonical before serial write")
	}
	if len(first.rootNodes) != initialSize {
		t.Fatal("serial membership miss changed historical catalog")
	}
	// Models setResultExprType's nonshared serial branch between synchronous
	// specialization calls. No partial catalog or negative survives publication.
	root.ExprTypes[unknown] = root.NamedTypes["i64"]
	secondCtx, secondPrivate := targetedMembershipFixture(t, root, fn)
	second := newSpecializationPublication(secondCtx, secondPrivate)
	if !second.canonicalNodeMember(unknown) {
		t.Fatal("new serial root row hidden by prior negative membership")
	}
	delete(root.ExprTypes, unknown)
	thirdCtx, thirdPrivate := targetedMembershipFixture(t, root, fn)
	third := newSpecializationPublication(thirdCtx, thirdPrivate)
	if third.canonicalNodeMember(unknown) {
		t.Fatal("deleted serial root row retained by prior positive membership")
	}
}

// The only provenance producers are pinned after review of reflect.New and
// reflect.MakeSlice-backed destinations. Borrowed-return branches must never
// register a destination; any producer edit requires repeating this proof.
func TestTargetedMembershipFreshSyntaxAllocationProducersRemainReviewed(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "specialization_syntax.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	parsed, err := parser.ParseFile(set, path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"copy": "503bd7f0584d5ce067b70aefc3168f49226c313d96d379ab9f0459796cd1d7e4", "allocateSlice": "b9d2b4b440c290bca97a16e28ccfd90d3b565da1123e6ae8e654238b91f82031"}
	writes := 0
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*goast.FuncDecl)
		if !ok {
			continue
		}
		if digest, ok := expected[fn.Name.Name]; ok {
			start, end := set.Position(fn.Pos()).Offset, set.Position(fn.End()).Offset
			if fmt.Sprintf("%x", sha256.Sum256(source[start:end])) != digest {
				t.Fatalf("review fresh allocation provenance before accepting %s", fn.Name.Name)
			}
		}
		goast.Inspect(fn.Body, func(node goast.Node) bool {
			assignment, ok := node.(*goast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assignment.Lhs {
				index, ok := lhs.(*goast.IndexExpr)
				if !ok {
					continue
				}
				selector, ok := index.X.(*goast.SelectorExpr)
				if !ok || selector.Sel.Name != "nodes" {
					continue
				}
				writes++
				if _, reviewed := expected[fn.Name.Name]; !reviewed {
					t.Fatalf("unreviewed syntax node allocation producer %s", fn.Name.Name)
				}
			}
			return true
		})
	}
	if writes != 2 {
		t.Fatalf("fresh node producer inventory changed: %d", writes)
	}
}
