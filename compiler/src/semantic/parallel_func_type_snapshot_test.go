package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestParallelPublishedFuncTypeDetachesMutableMetadata(t *testing.T) {
	for _, final := range []bool{false, true} {
		packet := &StructType{Name: "Packet"}
		expr := &ast.BoolLit{Value: true}
		nested := &FuncType{Name: "callback", Return: packet, SinkParams: []bool{true}}
		private := &FuncType{Name: "callee", Params: []Type{&RefType{Elem: nested}, packet}, Return: nested,
			SinkParams: []bool{true}, PermissionRefs: []ast.PermissionRef{{Name: "Read"}},
			ParamRefinementRanges:     map[int]numRange{0: {}},
			ReturnIsolation:           ReturnIsolationSummary{AliasParamIndices: []int{0}},
			ExplicitParamDefaultExprs: []ast.Expr{expr},
		}
		shared := &FuncType{Name: private.Name}
		if final {
			p := &parallelWorkerState{funcTypes: map[*FuncType]*FuncType{shared: private}, funcTypeOwned: map[*FuncType]bool{shared: true}}
			p.publishFuncTypes()
		} else {
			publishChangedFields(shared, private)
		}
		sharedNested := shared.Return.(*FuncType)
		sharedRefNested := shared.Params[0].(*RefType).Elem.(*FuncType)
		if sharedNested == nested || sharedRefNested == nested || sharedNested.Return != packet || shared.Params[1] != packet || shared.ExplicitParamDefaultExprs[0] != expr {
			t.Fatal("snapshot either leaked mutable nested function type or cloned nominal/AST identities")
		}
		private.SinkParams[0] = false
		private.PermissionRefs[0].Name = "Write"
		private.ReturnIsolation.AliasParamIndices[0] = 9
		delete(private.ParamRefinementRanges, 0)
		nested.SinkParams[0] = false
		if !shared.SinkParams[0] || shared.PermissionRefs[0].Name != "Read" || shared.ReturnIsolation.AliasParamIndices[0] != 0 || len(shared.ParamRefinementRanges) != 1 || !sharedNested.SinkParams[0] || !sharedRefNested.SinkParams[0] {
			t.Fatal("published summary shares mutable backing with its former owner")
		}
	}
}

func TestParallelSubstitutionRecognizesPrivateFunctionTypes(t *testing.T) {
	worker := &Analyzer{parallel: &parallelWorkerState{}}
	shared := &FuncType{Name: "callee", Return: &BuiltinType{Name: "bool"}, SinkParams: []bool{true}}
	local := worker.privateFuncType(nil, shared)
	if worker.privateFuncType(nil, local) != local || len(worker.parallel.funcTypes) != 1 {
		t.Fatal("private signature was copied as a new shared declaration")
	}
	applied := worker.substituteType(shared, nil, nil, nil, nil).(*FuncType)
	if worker.privateFuncType(nil, applied) != applied || len(worker.parallel.funcTypes) != 1 {
		t.Fatal("derived signature was not marked worker-local")
	}
	applied.SinkParams[0] = false
	if !shared.SinkParams[0] || !local.SinkParams[0] {
		t.Fatal("substitution result shares sink metadata with its source")
	}
}

// Inline groups have declaration identity even when their layout is identical.
// Cover the lazy snapshot, both publication paths, and call substitution: each
// crosses a different function-metadata copying boundary.
func TestParallelFunctionSnapshotPreservesBitGroupIdentity(t *testing.T) {
	group := &BitGroupType{Name: "Packet.flags"}
	other := &BitGroupType{Name: group.Name}
	if SameType(group, other) {
		t.Fatal("fixture requires distinct nominal inline groups")
	}
	nested := &FuncType{Params: []Type{group}, Return: group}
	source := &FuncType{Params: []Type{group, &RefType{Elem: nested}}, Return: group}
	worker := &Analyzer{parallel: &parallelWorkerState{}}
	lazy := worker.privateFuncType(nil, source)
	applied := worker.substituteType(source, nil, nil, nil, nil).(*FuncType)
	changed := &FuncType{}
	publishChangedFields(changed, source)
	final := &FuncType{}
	(&parallelWorkerState{funcTypes: map[*FuncType]*FuncType{final: source}, funcTypeOwned: map[*FuncType]bool{final: true}}).publishFuncTypes()
	for name, snapshot := range map[string]*FuncType{"lazy": lazy, "substituted": applied, "changed": changed, "final": final} {
		callback := snapshot.Params[1].(*RefType).Elem.(*FuncType)
		if snapshot.Params[0] != group || snapshot.Return != group || callback.Params[0] != group || callback.Return != group || !SameType(source, snapshot) {
			t.Fatalf("%s cloned a canonical inline group", name)
		}
	}
	// Reflection also traverses private fields of copied metadata, not just the
	// exported fields currently present in FuncType.
	type privateMetadata struct {
		group    Type
		callback *FuncType
	}
	value := snapshotFuncTypeMetadata(reflect.ValueOf(privateMetadata{group: group, callback: nested}))
	copied := value.Interface().(privateMetadata)
	if copied.group != group || copied.callback == nested || copied.callback.Return != group {
		t.Fatal("private metadata fields lost identity or isolation")
	}
}

func TestParallelOrdinaryGenericInlineGroupCall(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "4")
	analyzeFunctionAnalysisTestSource(t, "parallel-inline-group.elisa", `struct Packet:
    flags: bitset:
        ready
    bits: bitfield:
        tag: u4

def identity[T](value: T) -> T:
    return value

def direct(value: Packet) -> bool:
    return identity(value.flags).ready

def tag(value: Packet) -> u4:
    return identity(value.bits).tag

def probe(value: Packet) -> bool:
    return direct(value) and tag(value) == 0
`)
}

func TestParallelFunctionSnapshotPreservesRecursiveRoot(t *testing.T) {
	source := &FuncType{Name: "recursive"}
	callback := &FuncType{Name: "callback", Return: source, SinkParams: []bool{true}}
	source.Return = source
	source.Params = []Type{callback, callback}
	expected, expectedErr := TypeSymbolKey(source)
	// Recursive function arguments can fail closed in the symbol-key encoder.
	// Preserve that outcome as well as every explicit graph identity below.
	worker := &Analyzer{parallel: &parallelWorkerState{}}
	lazy := worker.privateFuncType(nil, source)
	changed := &FuncType{}
	publishChangedFields(changed, source)
	final := &FuncType{}
	(&parallelWorkerState{funcTypes: map[*FuncType]*FuncType{final: source}, funcTypeOwned: map[*FuncType]bool{final: true}}).publishFuncTypes()
	for name, snapshot := range map[string]*FuncType{"lazy": lazy, "changed": changed, "final": final} {
		nested := snapshot.Params[0].(*FuncType)
		if snapshot.Return != snapshot || nested.Return != snapshot || snapshot.Params[1] != nested || nested == callback {
			t.Fatalf("%s changed recursive root or shared callback graph identity", name)
		}
		actual, err := TypeSymbolKey(snapshot)
		if (err == nil) != (expectedErr == nil) || actual != expected {
			t.Fatalf("%s symbol identity outcome changed: original=%v snapshot=%v\n%s\n%s", name, expectedErr, err, expected, actual)
		}
		if err != nil && err.Error() != expectedErr.Error() {
			t.Fatalf("%s symbol-key error changed: original=%v snapshot=%v", name, expectedErr, err)
		}
	}
	callback.SinkParams[0] = false
	if !lazy.Params[0].(*FuncType).SinkParams[0] || !changed.Params[0].(*FuncType).SinkParams[0] || !final.Params[0].(*FuncType).SinkParams[0] {
		t.Fatal("cyclic snapshot leaked owner metadata")
	}
}

func TestParallelPublicationPreservesCrossFieldFunctionSharing(t *testing.T) {
	callback := &FuncType{Name: "callback", Return: &BuiltinType{Name: "bool"}}
	source := &FuncType{Params: []Type{callback}, Return: callback}
	shared := &FuncType{}
	publishChangedFields(shared, source)
	if shared.Params[0] != shared.Return || shared.Return == callback {
		t.Fatal("publication changed cross-field function sharing or leaked owner type")
	}
}
