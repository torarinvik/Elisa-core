//go:build cgo

package semantic

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

const functionProductSource = `def work(value: i64) -> i64:
    requires value >= 0
    ensure result >= 0
    return value + 1
`

func parseProductFunction(t *testing.T, source string) *ast.FuncDecl {
	t.Helper()
	l := lexer.New("fixture.elisa", []byte(source))
	tokens := l.Tokenize()
	if len(l.Errors()) != 0 {
		t.Fatal(l.Errors())
	}
	p := parser.New(tokens)
	file := p.ParseFile("fixture.elisa")
	if len(p.Errors()) != 0 {
		t.Fatal(p.Errors())
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			return fn
		}
	}
	t.Fatal("missing test function")
	return nil
}
func productContext() FunctionIdentityContext {
	return FunctionIdentityContext{Origin: "/project/input.elisa", QualifiedName: "M.work"}
}
func requireProductIdentity(t *testing.T, fn *ast.FuncDecl, scope FunctionIdentityContext) FunctionIdentity {
	t.Helper()
	identity, err := NewFunctionIdentity(fn, scope)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}
func TestFunctionIdentitySeparatesBodyFromInterface(t *testing.T) {
	original := parseProductFunction(t, functionProductSource)
	key := requireProductIdentity(t, original, productContext()).Key()
	changedBody := parseProductFunction(t, strings.Replace(functionProductSource, "value + 1", "value + 2", 1))
	if requireProductIdentity(t, changedBody, productContext()).Key() != key {
		t.Fatal("body edit changed declaration identity")
	}
	for _, source := range []string{
		strings.Replace(functionProductSource, "requires value >= 0", "requires value >= 1", 1),
		strings.Replace(functionProductSource, "ensure result >= 0", "ensure result >= 1", 1),
		strings.ReplaceAll(functionProductSource, "i64", "i32"),
	} {
		if requireProductIdentity(t, parseProductFunction(t, source), productContext()).Key() == key {
			t.Fatal("interface/contract change ignored")
		}
	}
	changedRegion := *original
	changedRegion.ReturnRegion = "source"
	if requireProductIdentity(t, &changedRegion, productContext()).Key() == key {
		t.Fatal("return-region contract ignored")
	}
	for _, scope := range []FunctionIdentityContext{
		{Origin: "/untrusted/copy.elisa", QualifiedName: "M.work"},
		{Origin: "/project/input.elisa", QualifiedName: "Other.work"},
		{Origin: "/project/input.elisa", QualifiedName: "M.work", ImplKey: "Eq[i64]"},
		{Origin: "/project/input.elisa", QualifiedName: "M.work", Specialization: []string{"i64"}},
		{Origin: "/project/input.elisa", QualifiedName: "M.work", Specialization: []string{"u64"}},
	} {
		if requireProductIdentity(t, original, scope).Key() == key {
			t.Fatal("resolution/provenance context ignored")
		}
	}
	a := productContext()
	a.ImplKey = "Eq[i64]"
	b := a
	b.ImplKey = "Eq[u64]"
	if requireProductIdentity(t, original, a).Key() == requireProductIdentity(t, original, b).Key() {
		t.Fatal("receiver implementations collided")
	}
	if _, err := NewFunctionIdentity(original, FunctionIdentityContext{}); err == nil {
		t.Fatal("missing provenance admitted")
	}
}

func TestFunctionProductAnchorsRebindCurrentPositions(t *testing.T) {
	previous := parseProductFunction(t, functionProductSource)
	index, err := IndexFunctionNodes(previous)
	if err != nil {
		t.Fatal(err)
	}
	statement := previous.Body[0].(*ast.ReturnStmt)
	anchor, err := index.Anchor(statement.Value)
	if err != nil {
		t.Fatal(err)
	}
	current := parseProductFunction(t, "\n\n"+functionProductSource)
	next, err := IndexFunctionNodes(current)
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := next.Rebind(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if rebound != current.Body[0].(*ast.ReturnStmt).Value {
		t.Fatal("anchor retained previous node identity")
	}
	oldPos, newPos := statement.Value.Pos(), rebound.Pos()
	if newPos.Line != oldPos.Line+2 || newPos.EndLine != oldPos.EndLine+2 || newPos.Offset != oldPos.Offset+2 || newPos.EndOffset != oldPos.EndOffset+2 {
		t.Fatalf("positions not rebound: %+v -> %+v", oldPos, newPos)
	}
	if requireProductIdentity(t, previous, productContext()).Key() != requireProductIdentity(t, current, productContext()).Key() {
		t.Fatal("source relocation changed stable identity")
	}
	// Replacing nodes with structurally identical current nodes must return the
	// replacements, not stale pointers retained by an existing index.
	oldNode := current.Body[0].(*ast.ReturnStmt).Value
	replacement := parseProductFunction(t, "\n\n\n"+functionProductSource)
	current.Body = replacement.Body
	refreshed, err := next.Rebind(anchor)
	if err != nil || refreshed == oldNode || refreshed.Pos().Line != oldPos.Line+3 {
		t.Fatalf("equivalent node replacement not rebound: %v", err)
	}
	// A real edit made after index construction must be detected as well.
	current.Body = parseProductFunction(t, strings.Replace(functionProductSource, "+ 1", "+ 2", 1)).Body
	if _, err := next.Rebind(anchor); err == nil {
		t.Fatal("mutated tree used a stale fingerprint")
	}
	anchor.Kind = "*ast.Ident"
	if _, err := index.Rebind(anchor); err == nil {
		t.Fatal("wrong node kind admitted")
	}
}

func TestFunctionProductAnchorsIncludeContractsAndSortedMaps(t *testing.T) {
	first := parseProductFunction(t, functionProductSource)
	call := &ast.CallExpr{Func: &ast.Ident{Name: "callee"}, ResolvedCommonArgs: map[string]ast.Expr{"z": &ast.IntLit{Value: "2"}, "a": &ast.IntLit{Value: "1"}}}
	first.Body = []ast.Stmt{&ast.ReturnStmt{Value: call}}
	index, err := IndexFunctionNodes(first)
	if err != nil {
		t.Fatal(err)
	}
	contractAnchor, err := index.Anchor(first.Requires[0])
	if err != nil {
		t.Fatal(err)
	}
	mapAnchor, err := index.Anchor(call.ResolvedCommonArgs["z"])
	if err != nil {
		t.Fatal(err)
	}
	second := parseProductFunction(t, "\n"+functionProductSource)
	otherCall := &ast.CallExpr{Func: &ast.Ident{Name: "callee"}, ResolvedCommonArgs: map[string]ast.Expr{}}
	otherCall.ResolvedCommonArgs["a"] = &ast.IntLit{Value: "1"}
	otherCall.ResolvedCommonArgs["z"] = &ast.IntLit{Value: "2"}
	second.Body = []ast.Stmt{&ast.ReturnStmt{Value: otherCall}}
	next, err := IndexFunctionNodes(second)
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := next.RebindAll([]FunctionNodeAnchor{contractAnchor, mapAnchor})
	if err != nil {
		t.Fatal(err)
	}
	if rebound[0] != second.Requires[0] || rebound[1] != otherCall.ResolvedCommonArgs["z"] {
		t.Fatal("contract or sorted map anchor omitted")
	}
}

func TestFunctionProductSyntaxDAGAndTypedNil(t *testing.T) {
	fn := parseProductFunction(t, "def work() -> i64:\n    return 1 + 1\n")
	binary := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	binary.Right = binary.Left
	index, err := IndexFunctionNodes(fn)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := index.Anchor(binary.Left)
	if err != nil {
		t.Fatal(err)
	}
	other := parseProductFunction(t, "def work() -> i64:\n    return 1 + 1\n")
	next, err := IndexFunctionNodes(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := next.Rebind(anchor); err == nil {
		t.Fatal("shared-subtree topology change accepted")
	}
	var missing *ast.IntLit
	binary.Right = missing
	if _, err := IndexFunctionNodes(fn); err != nil {
		t.Fatalf("typed nil not handled: %v", err)
	}
}

func TestFunctionProductResultFieldCoverage(t *testing.T) {
	requirements, err := FunctionProductRequirements()
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements) != reflect.TypeOf(Result{}).NumField() {
		t.Fatal("incomplete product field inventory")
	}
	typ := reflect.TypeOf(struct {
		Existing          bool
		NewSafetyMetadata bool
	}{})
	if err := validateFunctionProductFields(typ, map[string]FunctionProductDisposition{"Existing": FunctionProductInvocation}); err == nil || !strings.Contains(err.Error(), "NewSafetyMetadata") {
		t.Fatalf("new metadata did not close product gate: %v", err)
	}
}

func TestFunctionProductRejectsMutationBeforeAnchorCapture(t *testing.T) {
	fn := parseProductFunction(t, "def work() -> i64:\n    return 1\n")
	index, err := IndexFunctionNodes(fn)
	if err != nil {
		t.Fatal(err)
	}
	literal := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.IntLit)
	literal.Value = "2"
	if _, err := index.Anchor(literal); err == nil {
		t.Fatal("source-side mutation admitted stale anchor")
	}
	if _, err := index.Anchors([]PositionedFunctionNode{literal}); err == nil {
		t.Fatal("source-side mutation admitted stale batch")
	}
}

func TestFunctionProductSyntaxBudgetsDecline(t *testing.T) {
	fn := parseProductFunction(t, "def work() -> i64:\n    return 1\n")
	var expression ast.Expr = &ast.IntLit{Value: "1"}
	for i := 0; i < functionSyntaxMaxDepth; i++ {
		expression = &ast.ParenExpr{Inner: expression}
	}
	fn.Body = []ast.Stmt{&ast.ReturnStmt{Value: expression}}
	if _, err := IndexFunctionNodes(fn); !errors.Is(err, ErrFunctionSyntaxBudget) {
		t.Fatalf("deep graph not bounded: %v", err)
	}
	fn.Body = []ast.Stmt{&ast.ReturnStmt{Value: &ast.StringLit{Value: strings.Repeat("x", functionSyntaxMaxBytes+1)}}}
	if _, err := IndexFunctionNodes(fn); !errors.Is(err, ErrFunctionSyntaxBudget) {
		t.Fatalf("large encoding not bounded: %v", err)
	}
}
