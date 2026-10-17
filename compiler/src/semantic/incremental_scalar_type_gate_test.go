//go:build cgo

package semantic

import (
	"context"
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"strings"
	"testing"
)

func TestFunctionScalarTypeGateUsesActualCallerFacts(t *testing.T) {
	const source = `def callee(value: i64) -> i64:
    return value

def caller(value: i64) -> i64:
    return callee(value)
`
	file := parseIncrementalSessionFile(t, source)
	caller := file.Decls[1].(*ast.FuncDecl)
	index, err := IndexFunctionSource(caller, FunctionIdentityContext{Origin: "/project/calls.elisa", QualifiedName: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := index.Capture([]PositionedFunctionNode{caller})
	if err != nil {
		t.Fatal(err)
	}
	spec := functionTypeValueSpec{Site: anchors[0], Role: "function-types"}
	result := Analyze(file)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	callee := result.GlobalScope.Symbols["callee"].Type.(*FuncType)
	env, err := NewFunctionTypeEnvironmentFromResult([]FunctionTypeBinding{{Origin: "/project/calls.elisa", QualifiedName: "callee", Type: callee}}, result)
	if err != nil {
		t.Fatal(err)
	}
	input := FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{}}
	for node, typ := range result.ExprTypes {
		if _, owned := index.ordinals[node]; owned {
			input.ExprTypes[node] = typ
		}
	}
	facts, err := CaptureFunctionScalarFactsWithTypes(index, input, env)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := decodeScalarFacts(facts.Encoded())
	if err != nil || payload.Types == nil {
		t.Fatal(err)
	}
	owner, err := incremental.NewSession(functionTypeValueEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	publish := func(current FunctionScalarFacts) incremental.Revision {
		t.Helper()
		decoded, err := decodeScalarFacts(current.Encoded())
		if err != nil {
			t.Fatal(err)
		}
		typ, err := functionTypeValuePublication(spec, decoded.Types, env)
		if err != nil {
			t.Fatal(err)
		}
		gate, err := functionScalarTypeGatePublication(spec, current)
		if err != nil {
			t.Fatal(err)
		}
		world, err := owner.Publish(context.Background(), []incremental.Publication{typ, gate})
		if err != nil || len(world.Skipped) != 0 {
			t.Fatal(err, world.Skipped)
		}
		return world.Revision
	}
	key, _ := functionScalarTypeGateKey(spec)
	world := publish(facts)
	if encoded, err := world.Evaluate(context.Background(), key); err != nil || encoded != facts.Encoded() {
		t.Fatal("valid caller facts rejected:", err)
	}
	before := owner.Stats().Queries.Executed
	world = publish(facts)
	if _, err := world.Evaluate(context.Background(), key); err != nil || owner.Stats().Queries.Executed != before {
		t.Fatal("unchanged caller facts did not reuse memo:", err)
	}
	changedSource := strings.Replace(source, "def callee(value: i64)", "def callee(value: i64, extra: i64 = 0)", 1)
	changedFile := parseIncrementalSessionFile(t, changedSource)
	changedCaller := changedFile.Decls[1].(*ast.FuncDecl)
	changedIndex, err := IndexFunctionSource(changedCaller, FunctionIdentityContext{Origin: "/project/calls.elisa", QualifiedName: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	changedResult := Analyze(changedFile)
	if len(changedResult.Errors()) != 0 {
		t.Fatal(changedResult.Errors())
	}
	callee = changedResult.GlobalScope.Symbols["callee"].Type.(*FuncType)
	env, err = NewFunctionTypeEnvironmentFromResult([]FunctionTypeBinding{{Origin: "/project/calls.elisa", QualifiedName: "callee", Type: callee}}, changedResult)
	if err != nil {
		t.Fatal(err)
	}
	world = publish(facts)
	if _, err := world.Evaluate(context.Background(), key); err == nil {
		t.Fatal("old actual caller facts survived callee Type edit")
	}
	freshInput := FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{}}
	for node, typ := range changedResult.ExprTypes {
		if _, owned := changedIndex.ordinals[node]; owned {
			freshInput.ExprTypes[node] = typ
		}
	}
	fresh, err := CaptureFunctionScalarFactsWithTypes(changedIndex, freshInput, env)
	if err != nil {
		t.Fatal(err)
	}
	world = publish(fresh)
	encoded, err := world.Evaluate(context.Background(), key)
	if err != nil {
		t.Fatal("fresh facts did not recover:", err)
	}
	decoded, err := DecodeFunctionScalarFacts(encoded)
	if err != nil {
		t.Fatal(err)
	}
	target := parseIncrementalSessionFile(t, "\n\n"+changedSource).Decls[1].(*ast.FuncDecl)
	targetIndex, err := IndexFunctionSource(target, FunctionIdentityContext{Origin: "/project/calls.elisa", QualifiedName: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decoded.RestoreWithTypes(targetIndex, env)
	if err != nil {
		t.Fatal(err)
	}
	reference := target.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr).Func
	if restored.ExprTypes[reference] != callee {
		t.Fatal("gated facts lost current callee binding")
	}
	wrong := spec
	wrong.Site.FunctionKey = "another-owner"
	if _, err := functionScalarTypeGatePublication(wrong, facts); err == nil {
		t.Fatal("foreign caller facts admitted")
	}
}
