//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"encoding/json"
	"testing"
)

func currentCalleeType(t *testing.T, prefix string) (*Result, *FuncType, *FunctionTypeEnvironment) {
	t.Helper()
	result := Analyze(parseIncrementalSessionFile(t, prefix+"def callee(value: i64) -> i64:\n    return value\n"))
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	typ := result.GlobalScope.Symbols["callee"].Type.(*FuncType)
	env, err := NewFunctionTypeEnvironmentFromResult([]FunctionTypeBinding{{Origin: "/project/callee.elisa", QualifiedName: "callee", Type: typ}}, result)
	if err != nil {
		t.Fatal(err)
	}
	return result, typ, env
}

func TestFunctionTypeGraphConvergedCalleeRebindsCurrentResult(t *testing.T) {
	_, old, oldEnv := currentCalleeType(t, "")
	graph, err := captureFunctionTypeGraph(&OptionalType{Value: old}, oldEnv)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	var stored functionTypeGraph
	if err = json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	_, current, env := currentCalleeType(t, "\n\n")
	restored, err := stored.restore(env, false)
	if err != nil {
		t.Fatal(err)
	}
	if restored.(*OptionalType).Value != current || current == old {
		t.Fatal("callee reference did not bind fresh current function")
	}
	var key string
	for _, node := range stored.Nodes {
		if node.Reference != nil {
			key = node.Reference.Key
		}
	}
	dependency, err := env.CurrentDependency(key)
	if err != nil || dependency.Key != key {
		t.Fatalf("current callee dependency: %+v %v", dependency, err)
	}
}

func TestFunctionTypeGraphCalleeRequiresCompletedCurrentMembership(t *testing.T) {
	current, typ, _ := currentCalleeType(t, "")
	bindings := []FunctionTypeBinding{{Origin: "/project/callee.elisa", QualifiedName: "callee", Type: typ}}
	if _, err := NewFunctionTypeEnvironment(bindings); err == nil {
		t.Fatal("legacy environment admitted ungated function reference")
	}
	if _, err := NewFunctionTypeEnvironmentFromResult(bindings, nil); err == nil {
		t.Fatal("missing result admitted")
	}
	partial := analyzeWithEffectInference(parseIncrementalSessionFile(t, "def callee(value: i64) -> i64:\n    return value\n"), AnalyzeOptions{}, nil, func(*Analyzer, []scopedDecl) {})
	partialType := partial.GlobalScope.Symbols["callee"].Type
	if partial.PermissionEffectsConverged {
		t.Fatal("fixture unexpectedly completed effects")
	}
	if _, err := NewFunctionTypeEnvironmentFromResult([]FunctionTypeBinding{{Origin: "/project/callee.elisa", QualifiedName: "callee", Type: partialType}}, partial); err == nil {
		t.Fatal("partial effect rows admitted")
	}
	_, alien, _ := currentCalleeType(t, "")
	bindings[0].Type = alien
	if _, err := NewFunctionTypeEnvironmentFromResult(bindings, current); err == nil {
		t.Fatal("type from another Result admitted")
	}
}

func TestFunctionTypeGraphCalleeObservesLiveCompletionAndBinding(t *testing.T) {
	result, typ, env := currentCalleeType(t, "")
	graph, err := captureFunctionTypeGraph(typ, env)
	if err != nil {
		t.Fatal(err)
	}
	key := graph.Nodes[0].Reference.Key
	result.PermissionEffectsConverged = false
	if _, err := graph.restore(env, false); err == nil {
		t.Fatal("completion revocation ignored")
	}
	if _, err := env.CurrentDependency(key); err == nil {
		t.Fatal("stale dependency returned after revocation")
	}
	result.PermissionEffectsConverged = true
	if _, err := graph.restore(env, false); err != nil {
		t.Fatal("restored completion did not recover:", err)
	}
	original := result.GlobalScope.Symbols["callee"]
	delete(result.GlobalScope.Symbols, "callee")
	if _, err := graph.restore(env, false); err == nil {
		t.Fatal("removed callee retained authority")
	}
	result.GlobalScope.Symbols["callee"] = original
	copyType := *typ
	original.Type = &copyType
	if _, err := graph.restore(env, false); err == nil {
		t.Fatal("replaced declaration returned old Type")
	}
	original.Type = typ
}

func TestFunctionTypeGraphCalleeInterfaceEditsInvalidate(t *testing.T) {
	_, old, env := currentCalleeType(t, "")
	graph, err := captureFunctionTypeGraph(old, env)
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*FuncType){
		"return": func(f *FuncType) { f.Return = &BuiltinType{Name: "u32"} },
		"params": func(f *FuncType) { f.Params = []Type{&BuiltinType{Name: "u32"}} },
		"effects": func(f *FuncType) {
			f.Permissions = []string{"IO.Write"}
			f.PermissionRefs = []ast.PermissionRef{{Name: "IO.Write"}}
		},
		"owned":    func(f *FuncType) { f.OwnedParams = []bool{true} },
		"frame":    func(f *FuncType) { f.FrameBounded = true; f.FrameWrites = []FrameParamWrite{{ParamIndex: 0}} },
		"abi":      func(f *FuncType) { f.CallConv = "c"; f.IsNativeExtern = true },
		"async":    func(f *FuncType) { f.HasAsyncEntry = true },
		"defaults": func(f *FuncType) { f.ExplicitParamDefaultExprs = []ast.Expr{&ast.IntLit{Value: "7"}} },
	} {
		t.Run(name, func(t *testing.T) {
			_, current, currentEnv := currentCalleeType(t, "")
			edit(current)
			if _, err := graph.restore(currentEnv, false); err == nil {
				t.Fatal("changed callee interface reused")
			}
		})
	}
}

func TestFunctionTypeGraphNestedCalleeNeedsCompletion(t *testing.T) {
	result, callee, _ := currentCalleeType(t, "")
	structure := &StructType{Name: "Holder", Fields: map[string]Field{"callback": {Name: "callback", Type: callee}}}
	bindings := []FunctionTypeBinding{{Origin: "/project/callee.elisa", QualifiedName: "Holder", Type: structure}}
	legacy, err := NewFunctionTypeEnvironment(bindings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureFunctionTypeGraph(structure, legacy); err == nil {
		t.Fatal("nested function bypassed completion gate")
	}
	env, err := NewFunctionTypeEnvironmentFromResult(bindings, result)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := captureFunctionTypeGraph(structure, env)
	if err != nil {
		t.Fatal(err)
	}
	result.PermissionEffectsConverged = false
	if _, err := graph.restore(env, false); err == nil {
		t.Fatal("nested function ignored revoked completion")
	}
}

func TestFunctionTypeGraphCalleeBitSetsAreSemanticInputs(t *testing.T) {
	_, old, env := currentCalleeType(t, "")
	old.ReturnProvenance.ParamDeps = intBitSetOf(0, 65, 130)
	graph, err := captureFunctionTypeGraph(old, env)
	if err != nil {
		t.Fatal(err)
	}
	_, current, currentEnv := currentCalleeType(t, "")
	current.ReturnProvenance.ParamDeps = intBitSetOf(0, 65, 130)
	current.ReturnProvenance.ParamDeps.extra = append(current.ReturnProvenance.ParamDeps.extra, 0, 0)
	if _, err := graph.restore(currentEnv, false); err != nil {
		t.Fatal("zero word allocation changed logical set:", err)
	}
	current.ReturnProvenance.ParamDeps.Add(129)
	if _, err := graph.restore(currentEnv, false); err == nil {
		t.Fatal("overflow provenance bit change ignored")
	}
}

func TestFunctionScalarFactsCaptureAndRestoreActualCalleeType(t *testing.T) {
	const source = `def callee(value: i64) -> i64:
    return value

def caller(value: i64) -> i64:
    return callee(value)
`
	file := parseIncrementalSessionFile(t, source)
	caller := file.Decls[1].(*ast.FuncDecl)
	context := FunctionIdentityContext{Origin: "/project/calls.elisa", QualifiedName: "caller"}
	index, err := IndexFunctionSource(caller, context)
	if err != nil {
		t.Fatal(err)
	}
	result := Analyze(file)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	callee := result.GlobalScope.Symbols["callee"].Type
	env, err := NewFunctionTypeEnvironmentFromResult([]FunctionTypeBinding{{Origin: "/project/calls.elisa", QualifiedName: "callee", Type: callee}}, result)
	if err != nil {
		t.Fatal(err)
	}
	observations := FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{}}
	for node, typ := range result.ExprTypes {
		if _, owned := index.ordinals[node]; owned {
			observations.ExprTypes[node] = typ
		}
	}
	facts, err := CaptureFunctionScalarFactsWithTypes(index, observations, env)
	if err != nil {
		t.Fatal(err)
	}
	dependencies, err := facts.TypeDependencies()
	if err != nil || len(dependencies) != 1 {
		t.Fatalf("callee dependency omitted: %+v %v", dependencies, err)
	}
	current := Analyze(parseIncrementalSessionFile(t, "\n\n"+source))
	if len(current.Errors()) != 0 {
		t.Fatal(current.Errors())
	}
	currentCallee := current.GlobalScope.Symbols["callee"].Type
	currentEnv, err := NewFunctionTypeEnvironmentFromResult([]FunctionTypeBinding{{Origin: "/project/calls.elisa", QualifiedName: "callee", Type: currentCallee}}, current)
	if err != nil {
		t.Fatal(err)
	}
	dependency, err := currentEnv.CurrentDependency(dependencies[0].Key)
	if err != nil || dependency != dependencies[0] {
		t.Fatalf("fresh callee dependency changed: %+v %v", dependency, err)
	}
	target := parseIncrementalSessionFile(t, "\n\n"+source).Decls[1].(*ast.FuncDecl)
	targetIndex, err := IndexFunctionSource(target, context)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := facts.RestoreWithTypes(targetIndex, currentEnv)
	if err != nil {
		t.Fatal(err)
	}
	reference := target.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr).Func
	if restored.ExprTypes[reference] != currentCallee || currentCallee == callee {
		t.Fatal("actual function-valued expression kept previous invocation's Type")
	}
	currentCallee.(*FuncType).Permissions = []string{"IO.Write"}
	if _, err := facts.RestoreWithTypes(targetIndex, currentEnv); err == nil {
		t.Fatal("callee interface edit failed to invalidate caller facts")
	}
}

func TestFunctionTypeEnvironmentRejectsNonUTF8Identities(t *testing.T) {
	for _, binding := range []FunctionTypeBinding{
		{Origin: "\xff", QualifiedName: "Node", Type: &StructType{Name: "Node"}},
		{Origin: "/project/node.elisa", QualifiedName: "\xff", Type: &StructType{Name: "\xff"}},
	} {
		if _, err := NewFunctionTypeEnvironment([]FunctionTypeBinding{binding}); err == nil {
			t.Fatal("JSON replacement would conflate invalid identity bytes")
		}
	}
}
