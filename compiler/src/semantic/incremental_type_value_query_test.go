//go:build cgo

package semantic

import (
	"context"
	"elisacore/src/incremental"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func typeValueTestSpec(t *testing.T) functionTypeValueSpec {
	t.Helper()
	fn := parseProductFunction(t, "def work() -> i64:\n    return 1\n")
	index, err := IndexFunctionSource(fn, productContext())
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := index.Capture([]PositionedFunctionNode{fn})
	if err != nil {
		t.Fatal(err)
	}
	return functionTypeValueSpec{Site: anchors[0], Role: "callable-type"}
}
func TestFunctionTypeValueQueryInvalidatesStoredCallerProof(t *testing.T) {
	ctx := context.Background()
	spec := typeValueTestSpec(t)
	_, old, env := currentCalleeType(t, "")
	source, err := captureFunctionTypeGraph(old, env)
	if err != nil {
		t.Fatal(err)
	}
	oldFingerprint := source.Nodes[0].Reference.Fingerprint
	key, err := functionTypeValueKey(spec)
	if err != nil {
		t.Fatal(err)
	}
	body := incremental.Key{Kind: "stored-caller-proof", Name: "caller"}
	required := incremental.Key{Kind: "stored-caller-dependency", Name: "caller"}
	attempts := 0
	registry := functionTypeValueEvaluators()
	registry[body.Kind] = func(r *incremental.Reader, _ string) (string, error) {
		attempts++
		value, err := r.Query(key)
		if err != nil {
			return "", err
		}
		var graph functionTypeGraph
		if err := decodeFunctionTypeValue(value, &graph); err != nil {
			return "", err
		}
		fingerprint := graph.Nodes[0].Reference.Fingerprint
		expected, present := r.Input(required)
		if !present || expected != hex.EncodeToString(fingerprint[:]) {
			return "", fmt.Errorf("caller needs fresh typing")
		}
		return "typed", nil
	}
	owner, err := incremental.NewSession(registry, incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	publish := func(current *FunctionTypeEnvironment, fingerprint [32]byte) (incremental.Revision, incremental.Publication) {
		t.Helper()
		p, err := functionTypeValuePublication(spec, source, current)
		if err != nil {
			t.Fatal(err)
		}
		result, err := owner.Publish(ctx, []incremental.Publication{p, {Key: body, Inputs: []incremental.Change{{Key: required, Value: hex.EncodeToString(fingerprint[:])}}, MaxDependencies: 2, MaxResultBytes: 5}})
		if err != nil || len(result.Skipped) != 0 {
			t.Fatalf("publication: %v %v", err, result.Skipped)
		}
		return result.Revision, p
	}
	revision, first := publish(env, oldFingerprint)
	if value, err := revision.Evaluate(ctx, body); err != nil || value != "typed" {
		t.Fatal(value, err)
	}
	_, current, currentEnv := currentCalleeType(t, "\n\n")
	before := owner.Stats().Queries.Executed
	revision, again := publish(currentEnv, oldFingerprint)
	if _, err := revision.Evaluate(ctx, body); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || owner.Stats().Queries.Executed != before {
		t.Fatal("equal current declaration re-executed proof")
	}
	current.Permissions = []string{"IO.Write"}
	revision, changed := publish(currentEnv, oldFingerprint)
	if first.Inputs[1].Value != again.Inputs[1].Value || first.Inputs[1].Value != changed.Inputs[1].Value {
		t.Fatal("mutable fingerprints leaked into retained recipe")
	}
	if _, err := revision.Evaluate(ctx, body); err == nil {
		t.Fatal("changed callee reused old caller proof")
	}
	if _, err := revision.Evaluate(ctx, body); err == nil || attempts != 3 {
		t.Fatal("missing fresh typing was cached as success")
	}
	encoded, err := revision.Evaluate(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var graph functionTypeGraph
	if err := decodeFunctionTypeValue(encoded, &graph); err != nil {
		t.Fatal(err)
	}
	restored, err := graph.restore(currentEnv, false)
	if err != nil || restored != current {
		t.Fatalf("type product did not rebind current declaration: %v", err)
	}
	if source.Nodes[0].Reference.Fingerprint != oldFingerprint {
		t.Fatal("publication mutated retained fact")
	}
	dependency, err := currentEnv.CurrentDependency(source.Nodes[0].Reference.Key)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ = publish(currentEnv, dependency.Fingerprint)
	if _, err := revision.Evaluate(ctx, body); err != nil {
		t.Fatal("fresh caller proof did not recover:", err)
	}
}

func TestFunctionTypeValueEqualParentPrunesGrandparent(t *testing.T) {
	spec := typeValueTestSpec(t)
	_, typ, env := currentCalleeType(t, "")
	source, err := captureFunctionTypeGraph(typ, env)
	if err != nil {
		t.Fatal(err)
	}
	p, err := functionTypeValuePublication(spec, source, env)
	if err != nil {
		t.Fatal(err)
	}
	parent := incremental.Key{Kind: "type-kind", Name: "root"}
	grand := incremental.Key{Kind: "use-type-kind", Name: "root"}
	parentRuns, grandRuns := 0, 0
	registry := functionTypeValueEvaluators()
	registry[parent.Kind] = func(r *incremental.Reader, _ string) (string, error) {
		parentRuns++
		encoded, err := r.Query(p.Key)
		if err != nil {
			return "", err
		}
		var graph functionTypeGraph
		if err := decodeFunctionTypeValue(encoded, &graph); err != nil {
			return "", err
		}
		return graph.Nodes[graph.Root-1].Kind, nil
	}
	registry[grand.Kind] = func(r *incremental.Reader, _ string) (string, error) { grandRuns++; return r.Query(parent) }
	db := incremental.New(registry)
	db.Apply(p.Inputs...)
	if _, err := db.Evaluate(context.Background(), grand); err != nil {
		t.Fatal(err)
	}
	typ.HasAsyncEntry = true
	updated, err := functionTypeValuePublication(spec, source, env)
	if err != nil {
		t.Fatal(err)
	}
	db.Apply(updated.Inputs...)
	if _, err := db.Evaluate(context.Background(), grand); err != nil {
		t.Fatal(err)
	}
	if parentRuns != 2 || grandRuns != 1 {
		t.Fatalf("equal-output pruning failed: parent%d grandparent%d", parentRuns, grandRuns)
	}
}

func TestFunctionTypeValueReadinessRevocationAndRecovery(t *testing.T) {
	spec := typeValueTestSpec(t)
	result, typ, env := currentCalleeType(t, "")
	graph, err := captureFunctionTypeGraph(typ, env)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := incremental.NewSession(functionTypeValueEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	publish := func() (incremental.Published, error) {
		return owner.PublishPrepared(context.Background(), func() ([]incremental.Publication, error) {
			p, err := functionTypeValuePublication(spec, graph, env)
			if err != nil {
				return nil, err
			}
			return []incremental.Publication{p}, nil
		})
	}
	first, err := publish()
	if err != nil {
		t.Fatal(err)
	}
	key, _ := functionTypeValueKey(spec)
	if _, err := first.Revision.Evaluate(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	result.PermissionEffectsConverged = false
	if _, err := publish(); err == nil {
		t.Fatal("unconverged environment admitted")
	}
	if _, err := first.Revision.Evaluate(context.Background(), key); err == nil {
		t.Fatal("old world retained authority after rejected preparation")
	}
	result.PermissionEffectsConverged = true
	recovered, err := publish()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recovered.Revision.Evaluate(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionTypeValueMalformedInputsAndBounds(t *testing.T) {
	spec := typeValueTestSpec(t)
	_, typ, env := currentCalleeType(t, "")
	graph, err := captureFunctionTypeGraph(typ, env)
	if err != nil {
		t.Fatal(err)
	}
	p, err := functionTypeValuePublication(spec, graph, env)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []incremental.Change{
		{Key: functionTypeValueReadyInput(p.Key), Remove: true},
		{Key: functionTypeValueReadyInput(p.Key), Value: "partial"},
		{Key: p.Inputs[2].Key, Remove: true},
		{Key: p.Inputs[2].Key, Value: strings.Repeat("Z", 64)},
		{Key: functionTypeValueRecipeInput(p.Key), Value: p.Inputs[1].Value + " "},
	} {
		db := incremental.New(functionTypeValueEvaluators())
		db.Apply(p.Inputs...)
		db.Apply(change)
		if _, err := db.Evaluate(context.Background(), p.Key); err == nil {
			t.Fatalf("malformed type input admitted: %+v", change)
		}
		db.Apply(p.Inputs...)
		if _, err := db.Evaluate(context.Background(), p.Key); err != nil {
			t.Fatal("corrected inputs did not recover:", err)
		}
	}
	bad := &functionTypeGraph{Root: 1, Nodes: []functionTypeNode{{Kind: "BuiltinType", Data: json.RawMessage(strings.Repeat("x", functionScalarFactsMaxBytes+1))}}}
	if _, err := functionTypeValuePublication(spec, bad, nil); err == nil {
		t.Fatal("oversized recipe decoded before preflight rejection")
	}
	if _, err := functionTypeValuePublication(spec, graph, nil); err == nil {
		t.Fatal("declaration graph without current environment admitted")
	}
}

func TestFunctionTypeValueComposesWithLexicalQuery(t *testing.T) {
	graph, err := captureFunctionTypeGraph(&BuiltinType{Name: "i64"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	typ, err := functionTypeValuePublication(typeValueTestSpec(t), graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	lexical, err := lexicalPublication(lexicalTestSpec("symbol"), []lexicalFrameValue{{Present: true, Binding: "current-slot"}})
	if err != nil {
		t.Fatal(err)
	}
	registry := functionTypeValueEvaluators()
	for kind, evaluator := range lexicalQueryEvaluators() {
		registry[kind] = evaluator
	}
	owner, err := incremental.NewSession(registry, incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	world, err := owner.Publish(context.Background(), []incremental.Publication{typ, lexical})
	if err != nil || len(world.Skipped) != 0 {
		t.Fatal(err, world.Skipped)
	}
	for _, key := range []incremental.Key{typ.Key, lexical.Key} {
		if _, err := world.Revision.Evaluate(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	if owner.Stats().RetainedQueries != 2 {
		t.Fatal("type and lookup worlds retired each other")
	}
}

func TestFunctionTypeValueBatchSharesCurrentDeclarationInputs(t *testing.T) {
	spec := typeValueTestSpec(t)
	_, typ, env := currentCalleeType(t, "")
	graph, err := captureFunctionTypeGraph(typ, env)
	if err != nil {
		t.Fatal(err)
	}
	second := spec
	second.Role = "second-callable"
	requests := []functionTypeValueRequest{{Spec: second, Graph: graph}, {Spec: spec, Graph: graph}}
	batch, err := prepareFunctionTypeValueBatch(requests, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Publications) != 2 || batch.Declarations != 1 || batch.AccountedConstructionBytes == 0 {
		t.Fatalf("shared declaration input lost: %+v", batch)
	}
	if batch.Publications[0].Key.Name > batch.Publications[1].Key.Name {
		t.Fatal("batch admission order not deterministic")
	}
	owner, err := incremental.NewSession(functionTypeValueEvaluators(), incremental.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	world, err := owner.Publish(context.Background(), batch.Publications)
	if err != nil || len(world.Skipped) != 0 {
		t.Fatal(err, world.Skipped)
	}
	for _, p := range batch.Publications {
		if _, err := world.Revision.Evaluate(context.Background(), p.Key); err != nil {
			t.Fatal(err)
		}
	}
	oldInput := batch.Publications[0].Inputs[2].Value
	typ.HasAsyncEntry = true
	fresh, err := prepareFunctionTypeValueBatch(requests, env)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Publications[0].Inputs[2].Value == oldInput || batch.Publications[0].Inputs[2].Value != oldInput {
		t.Fatal("batch memo retained mutable declaration data across worlds")
	}
	if _, err := prepareFunctionTypeValueBatch([]functionTypeValueRequest{requests[0], requests[0]}, env); err == nil {
		t.Fatal("duplicate context admitted")
	}
}

func TestFunctionTypeValueRejectsInconsistentHistoricalReferences(t *testing.T) {
	spec := typeValueTestSpec(t)
	_, typ, env := currentCalleeType(t, "")
	graph, err := captureFunctionTypeGraph(typ, env)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := graph.Nodes[0]
	ref := *duplicate.Reference
	ref.Fingerprint[0] ^= 1
	duplicate.Reference = &ref
	graph.Nodes = append(graph.Nodes, duplicate)
	if _, err := functionTypeValuePublication(spec, graph, env); err == nil {
		t.Fatal("inconsistent stored reference graph silently normalized")
	}
}

func TestFunctionTypeValueRejectsDeclarationKindConfusion(t *testing.T) {
	spec := typeValueTestSpec(t)
	_, typ, env := currentCalleeType(t, "")
	graph, err := captureFunctionTypeGraph(typ, env)
	if err != nil {
		t.Fatal(err)
	}
	p, err := functionTypeValuePublication(spec, graph, env)
	if err != nil {
		t.Fatal(err)
	}
	db := incremental.New(functionTypeValueEvaluators())
	db.Apply(p.Inputs...)
	db.Apply(incremental.Change{Key: p.Inputs[2].Key, Value: "EnumType:" + strings.SplitN(p.Inputs[2].Value, ":", 2)[1]})
	if _, err := db.Evaluate(context.Background(), p.Key); err == nil {
		t.Fatal("fingerprint from another declaration kind admitted")
	}
	graph.Nodes[0].Kind = "EnumType"
	graph.Nodes[0].Reference.Kind = "EnumType"
	if _, err := functionTypeValuePublication(spec, graph, env); err == nil {
		t.Fatal("mislabeled retained reference admitted")
	}
}
