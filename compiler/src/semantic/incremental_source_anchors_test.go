//go:build cgo

package semantic

import (
	"encoding/json"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/frontendir"
	"elisacore/src/lexer"
)

func TestFunctionSourceAnchorsAcrossRealAnalysis(t *testing.T) {
	const source = `def work(value: i64) -> i64:
    first = value + 1
    return first + 2
`
	fn := parseProductFunction(t, source)
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	before := []PositionedFunctionNode{fn.Body[0], fn.Body[1], fn.Body[1].(*ast.ReturnStmt).Value}
	result := Analyze(&ast.File{Filename: "fixture.elisa", Decls: []ast.Decl{fn}})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	anchors, err := index.Capture(before)
	if err != nil {
		t.Fatal(err)
	}
	// Only owned values cross invocation boundaries, never the source index.
	encoded, err := json.Marshal(anchors)
	if err != nil {
		t.Fatal(err)
	}
	anchors = nil
	if err := json.Unmarshal(encoded, &anchors); err != nil {
		t.Fatal(err)
	}
	freshSource := "\n\n" + strings.Replace(source, "    return", "\n    # internal relocation\n    return", 1)
	fresh := parseProductFunction(t, freshSource)
	target, err := IndexFunctionSource(fresh, productContext())
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := target.Rebind(anchors)
	if err != nil {
		t.Fatal(err)
	}
	want := []PositionedFunctionNode{fresh.Body[0], fresh.Body[1], fresh.Body[1].(*ast.ReturnStmt).Value}
	for i, node := range rebound {
		if node != want[i] || node == before[i] || node.Pos() != want[i].Pos() {
			t.Fatalf("stale node/span at %d", i)
		}
	}
	if rebound[1].Pos().Line-before[1].Pos().Line == rebound[0].Pos().Line-before[0].Pos().Line {
		t.Fatal("fixture must exercise unequal position shifts")
	}
	for _, scope := range []FunctionIdentityContext{
		{Origin: "/other/input.elisa", QualifiedName: "M.work"},
		{Origin: "/project/input.elisa", QualifiedName: "Other.work"},
	} {
		other, err := IndexFunctionSource(fresh, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Rebind(anchors); err == nil {
			t.Fatal("cross-origin anchor accepted")
		}
	}
	changed, err := IndexFunctionSource(parseProductFunction(t, strings.Replace(source, "+ 2", "+ 3", 1)), productContext())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changed.Rebind(anchors); err == nil {
		t.Fatal("changed body accepted")
	}
}

type invalidSourceNode []int

func (invalidSourceNode) Pos() lexer.Pos { return lexer.Pos{} }

func TestFunctionSourceAnchorsDeclineInvalidAndTransportNormalizedNodes(t *testing.T) {
	fn := parseProductFunction(t, functionProductSource)
	// This shape is legal to construct but the transport normalizes it to nil.
	fn.Requires = []ast.Expr{}
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := index.Capture([]PositionedFunctionNode{fn})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []PositionedFunctionNode{nil, (*ast.IntLit)(nil), invalidSourceNode{1}} {
		if _, err := index.Capture([]PositionedFunctionNode{node}); err == nil {
			t.Fatal("invalid node accepted")
		}
	}
	data, err := frontendir.Encode(&frontendir.Bundle{File: &ast.File{Decls: []ast.Decl{fn}}})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := frontendir.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	target, err := IndexFunctionSource(bundle.File.Decls[0].(*ast.FuncDecl), productContext())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Rebind(anchors); err == nil {
		t.Fatal("normalized transport shape silently accepted")
	}
}

func TestFunctionSourceAnchorsRejectGeneratedDefaultAndMutatedTarget(t *testing.T) {
	const source = `def add(x: i64, y: i64 = 7) -> i64:
    return x + y

def build() -> i64:
    return add(5)
`
	// Parse both declarations using the existing parser helper for each suffix.
	callee := parseProductFunction(t, source)
	caller := parseProductFunction(t, source[strings.Index(source, "def build"):])
	index, err := IndexFunctionSource(caller, FunctionIdentityContext{Origin: "/project/input.elisa", QualifiedName: "build"})
	if err != nil {
		t.Fatal(err)
	}
	call := caller.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	originalArg := call.Args[0]
	result := Analyze(&ast.File{Filename: "fixture.elisa", Decls: []ast.Decl{callee, caller}})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 2 {
		t.Fatal("analysis did not resolve default")
	}
	anchors, err := index.Capture([]PositionedFunctionNode{call, originalArg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Capture([]PositionedFunctionNode{call.ResolvedArgs[1]}); err == nil {
		t.Fatal("generated default acquired caller source identity")
	}
	if _, err := index.Rebind(anchors); err == nil {
		t.Fatal("analyzed target accepted as pristine")
	}
	// Replacing a source node must not allow the old detached pointer to escape.
	caller.Body = nil
	if _, err := index.Capture([]PositionedFunctionNode{call}); err == nil {
		t.Fatal("detached source node accepted")
	}
}
