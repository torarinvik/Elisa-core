package semantic

import (
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
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
		{Kind: FactTransformRecompute, Classes: []FactClass{FactTypestate, FactShape, FactOptimization}, Target: "player.health", Source: "control-flow instruction", SourceKind: FactSourceFlowInstr, Details: []FactTransformDetail{{Name: "mutation", Value: "assign"}}, Reason: "assign"},
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

func TestFactTransformsFromGenericInterfaceBounds(t *testing.T) {
	fn := &ast.FuncDecl{Name: "build", GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "B", InterfaceBound: "Builder"}}}
	got := factTransformsFromGenericInterfaceBounds(fn)
	want := []FactTransform{{Kind: FactTransformRequire, Classes: []FactClass{FactInterface}, Target: "B:Builder", Source: "generic parameter", SourceKind: FactSourceSignature, Reason: "required interface fact"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected interface-bound fact transforms:\nwant %#v\n got %#v", want, got)
	}
}

func TestFunctionFactSnapshotPreservesInterfaceFactsAcrossJoin(t *testing.T) {
	transforms := []FactTransform{
		{Kind: FactTransformRequire, Classes: []FactClass{FactInterface}, Target: "B:Builder", Source: "generic parameter", SourceKind: FactSourceSignature, Reason: "required interface fact"},
		{Kind: FactTransformRequire, Classes: []FactClass{FactInterface}, Target: "B:Builder", Source: "generic parameter", SourceKind: FactSourceSignature, Reason: "required interface fact"},
		{Kind: FactTransformRefine, Classes: []FactClass{FactOptimization}, Target: "branch", Source: "control-flow guard", SourceKind: FactSourceGuard, Reason: "join smoke"},
	}
	snapshot := buildFunctionFactSnapshot(&FuncType{Return: &BuiltinType{Name: "int"}}, nil, transforms, nil)
	if !reflect.DeepEqual(snapshot.RequiredInterfaces, []string{"B:Builder"}) {
		t.Fatalf("expected joined interface fact to be preserved once, got %#v", snapshot.RequiredInterfaces)
	}
}

func TestFunctionFactSnapshotKeepsInterfaceFactsWhenTypestateWidens(t *testing.T) {
	transforms := []FactTransform{
		{Kind: FactTransformRequire, Classes: []FactClass{FactInterface}, Target: "B:Builder", Source: "generic parameter", SourceKind: FactSourceSignature, Reason: "required interface fact"},
		{Kind: FactTransformWiden, Classes: []FactClass{FactTypestate}, Target: "player", Source: "unknown_update(player)", SourceKind: FactSourceCallWiden, Reason: "ref call without matching ensures"},
	}
	snapshot := buildFunctionFactSnapshot(&FuncType{Return: &BuiltinType{Name: "int"}}, nil, transforms, nil)
	if !reflect.DeepEqual(snapshot.RequiredInterfaces, []string{"B:Builder"}) {
		t.Fatalf("expected interface facts to survive typestate widening, got %#v", snapshot.RequiredInterfaces)
	}
	if !reflect.DeepEqual(snapshot.Widened, []string{"player"}) {
		t.Fatalf("expected widened typestate target, got %#v", snapshot.Widened)
	}
}

func TestFunctionFactExitSummarySeparatesInterfaceRequirementsFromExitFacts(t *testing.T) {
	transforms := []FactTransform{
		{Kind: FactTransformRequire, Classes: []FactClass{FactInterface}, Target: "B:Builder", Source: "generic parameter", SourceKind: FactSourceSignature, Reason: "required interface fact"},
		{Kind: FactTransformEnsure, Classes: []FactClass{FactTypestate}, Target: "job", Source: "ensures always", SourceKind: FactSourceSignature, Reason: "ensures typestate Ready"},
		{Kind: FactTransformProduce, Classes: []FactClass{FactErrorPath}, Target: "<error>", Source: "ParseError.Bad", SourceKind: FactSourceErrorPath, Reason: "raise error path"},
	}
	summary := buildFunctionFactExitSummary(transforms)
	if len(summary.Normal) != 1 || !strings.Contains(summary.Normal[0], "ensure job [typestate]") {
		t.Fatalf("expected normal ensure exit fact only, got %#v", summary.Normal)
	}
	if len(summary.Error) != 1 || !strings.Contains(summary.Error[0], "produce <error> [error-path]") {
		t.Fatalf("expected error path fact only, got %#v", summary.Error)
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

func TestFlowInstrProduceDetailsDoNotInventNodeProvenance(t *testing.T) {
	transform, ok := factTransformFromFlowInstr(FlowInstr{Kind: FlowInstrProduce, Location: "value", Source: "expr_store", Note: "allocation produces value"})
	if !ok {
		t.Fatal("expected allocation flow instruction to produce a fact transform")
	}
	var wantDetails []FactTransformDetail
	if !reflect.DeepEqual(transform.Details, wantDetails) {
		t.Fatalf("expected no node-specific details\nwant %#v\n got %#v", wantDetails, transform.Details)
	}
}
