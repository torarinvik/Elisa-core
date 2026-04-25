package semantic

import (
	"reflect"
	"testing"

	"llcontext/src/ast"
)

func TestFactTransformsFromCFGFlowInstrsProjectsAllCoreInstructionKinds(t *testing.T) {
	cfg := &CFG{Blocks: []CFGBlock{{Instrs: []FlowInstr{
		{Kind: FlowInstrAlias, Location: "alias", Source: "first", Note: "var init alias"},
		{Kind: FlowInstrAlias, Location: "alias", Source: "first", Note: "var init alias"},
		{Kind: FlowInstrAlias, Location: "missing_source", Note: "ignored"},
		{Kind: FlowInstrConsume, Location: "thread", Note: "explicit move"},
		{Kind: FlowInstrInvalidate, Location: "scratch", Source: "cp", Note: "restore region checkpoint"},
		{Kind: FlowInstrMutate, Location: "player.health", Note: "assign"},
		{Kind: FlowInstrProduce, Location: "first", Source: "scratch", Note: "allocation produces value"},
		{Kind: FlowInstrRebase, Location: "store", Note: "freeze rebases store provenance"},
	}}}}

	got := factTransformsFromCFGFlowInstrs(cfg)

	want := []FactTransform{
		{Kind: FactTransformConsume, Classes: []FactClass{FactUsage}, Target: "thread", Source: "control-flow instruction", SourceKind: FactSourceFlowInstr, Reason: "explicit move"},
		{Kind: FactTransformInvalidate, Classes: []FactClass{FactRegionDeps}, Target: "scratch", Source: "cp", SourceKind: FactSourceRegion, Details: []FactTransformDetail{{Name: "operation", Value: "restore region checkpoint"}, {Name: "checkpoint", Value: "cp"}}, Reason: "restore region checkpoint"},
		{Kind: FactTransformProduce, Classes: []FactClass{FactRepresentation, FactStorage}, Target: "first", Source: "scratch", SourceKind: FactSourceFlowInstr, Reason: "allocation produces value"},
		{Kind: FactTransformRebase, Classes: []FactClass{FactStoreDeps}, Target: "store", Source: "control-flow instruction", SourceKind: FactSourceStore, Details: []FactTransformDetail{{Name: "operation", Value: "freeze"}, {Name: "before", Value: "store"}, {Name: "after", Value: "frozen/public store"}}, Reason: "freeze rebases store provenance"},
		{Kind: FactTransformRecompute, Classes: []FactClass{FactTypestate}, Target: "player.health", Source: "control-flow instruction", SourceKind: FactSourceFlowInstr, Reason: "assign"},
		{Kind: FactTransformRefine, Classes: []FactClass{FactAliasClass}, Target: "alias", Source: "first", SourceKind: FactSourceFlowInstr, Details: []FactTransformDetail{{Name: "alias_member", Value: "first"}}, Reason: "var init alias"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected projected transforms:\nwant %#v\n got %#v", want, got)
	}
}

func TestFactTransformsFromCFGGuardsProjectsGuardFacts(t *testing.T) {
	guards := NewGuardFactSet()
	guards.AddNonNull(&ast.Ident{Name: "box"})
	guards.AddVariantProof(&ast.Ident{Name: "node"}, "Expr", "Int")
	guards.AddLE(&ast.Ident{Name: "start"}, &ast.Ident{Name: "end"})
	cfg := &CFG{Blocks: []CFGBlock{{Edges: []FlowEdge{{To: 1, Guard: guards}}}}}

	got := factTransformsFromCFGGuards(cfg)

	want := []FactTransform{
		{Kind: FactTransformRefine, Classes: []FactClass{FactRefState}, Target: "box", Source: "control-flow guard", SourceKind: FactSourceGuard, Reason: "guard proves non-null"},
		{Kind: FactTransformRefine, Classes: []FactClass{FactTypestate}, Target: "node", Source: "control-flow guard", SourceKind: FactSourceGuard, Reason: "guard proves variant Expr.Int"},
		{Kind: FactTransformRefine, Classes: []FactClass{FactOptimization}, Target: "start", Source: "control-flow guard", SourceKind: FactSourceGuard, Reason: "guard proves <= end"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected guard transforms:\nwant %#v\n got %#v", want, got)
	}
}

func TestFactTransformsFromFunctionSignatureProjectsPoststatesAndPermissions(t *testing.T) {
	fnType := &FuncType{
		Params:             []Type{&BuiltinType{Name: "Player"}, &RefType{Elem: &BuiltinType{Name: "i32"}}},
		ExplicitParamNames: []string{"player", "slot"},
		Poststates: []FuncPoststate{
			{ParamIndex: 0, Kind: FuncPoststateKindNamedState, StateCases: []string{"Alive"}},
			{ParamIndex: 1, Kind: FuncPoststateKindRefState, RefState: RefStateNonNull, Condition: FuncPoststateCondition{Kind: FuncPoststateConditionReturnBool, ReturnBool: true}},
			{ParamIndex: 0, Kind: FuncPoststateKindPreserve, Condition: FuncPoststateCondition{Kind: FuncPoststateConditionReturnBool, ReturnBool: false}},
		},
		PermissionRefs: []ast.PermissionRef{{Name: "Thread", Member: "Join"}},
	}

	poststates := factTransformsFromPoststates(fnType)
	wantPoststates := []FactTransform{
		{Kind: FactTransformEnsure, Classes: []FactClass{FactTypestate}, Target: "player", Source: "ensures always", SourceKind: FactSourceSignature, Reason: "ensures typestate Alive"},
		{Kind: FactTransformEnsure, Classes: []FactClass{FactTypestate, FactRefState}, Target: "player", Source: "ensures return false", SourceKind: FactSourceSignature, Reason: "ensures preserve"},
		{Kind: FactTransformEnsure, Classes: []FactClass{FactRefState}, Target: "slot", Source: "ensures return true", SourceKind: FactSourceSignature, Reason: "ensures refstate &"},
	}
	if !reflect.DeepEqual(poststates, wantPoststates) {
		t.Fatalf("unexpected poststate transforms:\nwant %#v\n got %#v", wantPoststates, poststates)
	}

	permissions := factTransformsFromPermissions(fnType)
	wantPermissions := []FactTransform{{
		Kind:       FactTransformRequire,
		Classes:    []FactClass{FactEffects},
		Target:     "Thread.Join",
		Source:     "function signature",
		SourceKind: FactSourcePermission,
		Reason:     "requires effect authority",
	}}
	if !reflect.DeepEqual(permissions, wantPermissions) {
		t.Fatalf("unexpected permission transforms:\nwant %#v\n got %#v", wantPermissions, permissions)
	}
}

func TestFactTransformsFromCFGFlowInstrsProjectsErrorAndReturnPaths(t *testing.T) {
	cfg := &CFG{Blocks: []CFGBlock{{Instrs: []FlowInstr{
		{Kind: FlowInstrErrorExit, Location: "<error>", Source: "checked", Note: "try propagates error path"},
		{Kind: FlowInstrReturn, Note: "return"},
	}}}}

	got := factTransformsFromCFGFlowInstrs(cfg)
	want := []FactTransform{
		{Kind: FactTransformProduce, Classes: []FactClass{FactErrorPath}, Target: "<error>", Source: "checked", SourceKind: FactSourceErrorPath, Reason: "try propagates error path"},
		{Kind: FactTransformProduce, Classes: []FactClass{FactRepresentation}, Target: "<return>", Source: "return statement", SourceKind: FactSourceReturn, Reason: "return"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected error/return path transforms:\nwant %#v\n got %#v", want, got)
	}
}
