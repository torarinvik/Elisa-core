package semantic

import (
	"elisacore/src/ast"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func codegenReadSchema(t *testing.T) []struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Analyzer *string `json:"analyzer"`
} {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../scripts/generate_specialization_accessors.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Fields []struct {
			Name     string  `json:"name"`
			Type     string  `json:"type"`
			Analyzer *string `json:"analyzer"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	return schema.Fields
}

func TestCodegenReadViewCoversActualSparseAccessorSchema(t *testing.T) {
	resultType, viewType := reflect.TypeFor[*Result](), reflect.TypeFor[CodegenReadView]()
	methods := 0
	for _, name := range []string{
		"ExprOptimizationFacts",
		"ExprsHaveSameExtent",
		"ExprsHaveEqualExtentSize",
		"ExprsAreDisjoint",
		"ExprSupportsDenseWrite",
		"ExprPackedStoreProvenance",
		"ExprHasPackedStoreProvenance",
		"ExprDependsOnFrozenPackedStores",
		"ExprDependsOnNonFrozenPackedStores",
		"ExprHasOnlyFrozenPackedStoreDeps",
		"ExprHasMixedPackedStoreProvenance",
		"ExprDependsOnlyOnFrozenPackedStores",
	} {
		original, ok := resultType.MethodByName(name)
		if !ok {
			t.Fatalf("Result missing derived optimization method %s", name)
		}
		wrapped, ok := viewType.MethodByName(name)
		if !ok {
			t.Fatalf("CodegenReadView missing derived optimization method %s", name)
		}
		if original.Type.NumIn() != wrapped.Type.NumIn() || original.Type.NumOut() != wrapped.Type.NumOut() {
			t.Fatalf("derived method signature mismatch %s", name)
		}
		for i := 1; i < original.Type.NumIn(); i++ {
			if original.Type.In(i) != wrapped.Type.In(i) {
				t.Fatalf("derived method input mismatch %s", name)
			}
		}
		for i := 0; i < original.Type.NumOut(); i++ {
			if original.Type.Out(i) != wrapped.Type.Out(i) {
				t.Fatalf("derived method output mismatch %s", name)
			}
		}
		methods++
	}
	for _, field := range codegenReadSchema(t) {
		if field.Analyzer == nil {
			continue
		}
		for _, name := range []string{"Lookup" + field.Name, "Value" + field.Name, "Range" + field.Name, field.Name + "Len"} {
			methods++
			original, ok := resultType.MethodByName(name)
			if !ok {
				t.Fatalf("schema missing Result method %s", name)
			}
			wrapped, ok := viewType.MethodByName(name)
			if !ok {
				t.Fatalf("schema missing observed method %s", name)
			}
			if original.Type.NumIn() != wrapped.Type.NumIn() || original.Type.NumOut() != wrapped.Type.NumOut() {
				t.Fatalf("signature mismatch %s", name)
			}
			for i := 1; i < original.Type.NumIn(); i++ {
				if original.Type.In(i) != wrapped.Type.In(i) {
					t.Fatalf("input type mismatch %s", name)
				}
			}
			for i := 0; i < original.Type.NumOut(); i++ {
				if original.Type.Out(i) != wrapped.Type.Out(i) {
					t.Fatalf("output type mismatch %s", name)
				}
			}
		}
		for _, own := range []struct {
			name   string
			output reflect.Type
		}{
			{"HasOwn" + field.Name + "Map", reflect.TypeFor[bool]()},
			{"Own" + field.Name + "Map", func() reflect.Type {
				f, ok := reflect.TypeFor[Result]().FieldByName(field.Name)
				if !ok {
					t.Fatal("missing physical table", field.Name)
				}
				return f.Type
			}()},
		} {
			methods++
			method, ok := viewType.MethodByName(own.name)
			if !ok || method.Type.NumIn() != 1 || method.Type.NumOut() != 1 || method.Type.Out(0) != own.output {
				t.Fatalf("physical map signature mismatch %s", own.name)
			}
		}
	}
	if methods != viewType.NumMethod() {
		t.Fatalf("unreviewed view methods: schema=%d view=%d", methods, viewType.NumMethod())
	}
}

func TestCodegenReadViewEverySchemaTablePreservesStoredZeroAndMissing(t *testing.T) {
	for _, field := range codegenReadSchema(t) {
		if field.Analyzer == nil {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			root := &Result{}
			table := reflect.ValueOf(root).Elem().FieldByName(field.Name)
			key, value := reflect.Zero(table.Type().Key()), reflect.Zero(table.Type().Elem())
			var events []CodegenFactRead
			view := NewCodegenReadView(root, func(read CodegenFactRead) { events = append(events, read) })
			lookup := reflect.ValueOf(view).MethodByName("Lookup" + field.Name)
			got := lookup.Call([]reflect.Value{key})
			if got[1].Bool() || len(events) != 1 || events[0].Present || events[0].Result != root {
				t.Fatal("missing lookup changed membership/context")
			}
			table.Set(reflect.MakeMap(table.Type()))
			table.SetMapIndex(key, value)
			events = nil
			got = lookup.Call([]reflect.Value{key})
			if !got[1].Bool() || !reflect.DeepEqual(got[0].Interface(), value.Interface()) || !events[0].Present {
				t.Fatal("stored zero/nil/false lost membership")
			}
			reflect.ValueOf(view).MethodByName("Value" + field.Name).Call([]reflect.Value{key})
			if len(events) != 2 || events[1].Operation != "value" || !events[1].Present {
				t.Fatal("value event changed lookup membership")
			}
			size := reflect.ValueOf(view).MethodByName(field.Name + "Len").Call(nil)
			if size[0].Int() != 1 || events[2].Operation != "len" {
				t.Fatal("logical length changed")
			}
			rangeMethod := reflect.ValueOf(view).MethodByName("Range" + field.Name)
			callbackType := rangeMethod.Type().In(0)
			count := 0
			callback := reflect.MakeFunc(callbackType, func(args []reflect.Value) []reflect.Value { count++; return []reflect.Value{reflect.ValueOf(true)} })
			rangeMethod.Call([]reflect.Value{callback})
			if count != 1 || len(events) != 5 || events[3].Operation != "range-item" || !events[3].Present || events[4].Operation != "range-end" || !events[4].Complete {
				t.Fatalf("logical range observations lost: %+v", events)
			}
			for _, event := range events {
				if event.Table != field.Name || event.Result != root {
					t.Fatal("event lost receiver/table context")
				}
			}
		})
	}
}

func TestCodegenReadViewPreservesPrivateAliasesOwnedMissesAndProofMask(t *testing.T) {
	original, private, foreign := &ast.Ident{Name: "original"}, &ast.Ident{Name: "private"}, &ast.Ident{Name: "foreign"}
	typ := &BuiltinType{Name: "i64"}
	index := &ast.IndexExpr{}
	root := &Result{ExprTypes: map[ast.Expr]Type{original: typ, foreign: typ}, IndexBoundsProven: map[*ast.IndexExpr]bool{index: true}}
	current := &Result{specializationRoot: root, specializationNodes: map[ast.Node]ast.Node{original: private}, specializationOwned: map[ast.Node]bool{private: true}, specializationProofMask: map[*ast.IndexExpr]bool{index: true}}
	var events []CodegenFactRead
	view := NewCodegenReadView(current, func(read CodegenFactRead) { events = append(events, read) })
	if _, present := view.LookupExprTypes(original); present {
		t.Fatal("owned missing alias revived template row")
	}
	if _, present := view.LookupExprTypes(private); present {
		t.Fatal("private missing key revived template row")
	}
	if value, present := view.LookupExprTypes(foreign); !present || value != typ {
		t.Fatal("foreign base row lost")
	}
	if _, present := view.LookupIndexBoundsProven(index); present {
		t.Fatal("masked proof revived")
	}
	current.ExprTypes = map[ast.Expr]Type{private: nil}
	if value, present := view.LookupExprTypes(original); !present || value != nil {
		t.Fatal("stored private nil lost membership")
	}
	for _, event := range events {
		if event.Result != current {
			t.Fatal("inherited read lost actual private Result context")
		}
	}
	// A fresh receiver follows a restored canonical context; the old value remains
	// attached to its private context without modifying a shared Result observer.
	restored := NewCodegenReadView(root, nil)
	if restored.ValueExprTypes(original) != typ || view.ValueExprTypes(original) != nil {
		t.Fatal("receiver context leaked across install/restore")
	}
}

func TestCodegenReadViewRangeCompletionAndDisabledParity(t *testing.T) {
	key := &ast.Ident{}
	root := &Result{ExprTypes: map[ast.Expr]Type{key: nil}}
	var events []CodegenFactRead
	view := NewCodegenReadView(root, func(read CodegenFactRead) { events = append(events, read) })
	view.RangeExprTypes(func(ast.Expr, Type) bool { return false })
	if len(events) != 2 || events[1].Complete {
		t.Fatal("early stop claimed full range")
	}
	events = nil
	view.RangeExprTypes(nil)
	if len(events) != 1 || events[0].Complete {
		t.Fatal("nil callback claimed full range")
	}
	events = nil
	func() {
		defer func() {
			if recover() == nil {
				t.Error("caller panic was swallowed")
			}
		}()
		view.RangeExprTypes(func(ast.Expr, Type) bool { panic("caller") })
	}()
	if len(events) != 2 || events[1].Complete {
		t.Fatal("panic claimed full range")
	}
	events = nil
	NewCodegenReadView(nil, view.Observe).RangeExprTypes(func(ast.Expr, Type) bool { return true })
	if len(events) != 1 || !events[0].Complete {
		t.Fatal("empty nil Result range did not complete")
	}
	plain := NewCodegenReadView(root, nil)
	if value, present := plain.LookupExprTypes(key); !present || value != nil {
		t.Fatal("disabled observer changed stored nil")
	}
	count := 0
	plain.RangeExprTypes(func(ast.Expr, Type) bool { count++; return false })
	if count != 1 || plain.ExprTypesLen() != root.ExprTypesLen() {
		t.Fatal("disabled observer changed range/length")
	}
}

func TestCodegenReadViewOptimizationQueriesMatchResult(t *testing.T) {
	left := &ast.Ident{Name: "left"}
	right := &ast.Ident{Name: "right"}
	mixed := &ast.Ident{Name: "mixed"}
	missing := &ast.Ident{Name: "missing"}
	canonical := &ast.Ident{Name: "canonical"}
	private := &ast.Ident{Name: "private"}
	foreign := &ast.Ident{Name: "foreign"}
	ownedMiss := &ast.Ident{Name: "owned-miss"}
	extent := &OptimizationExtent{Kind: OptimizationExtentArraySize, HasConstSize: true, ConstSize: 4}
	otherExtent := &OptimizationExtent{Kind: OptimizationExtentArraySize, HasConstSize: true, ConstSize: 9}
	leftFacts := OptimizationFacts{
		Exclusive: true, Contiguous: true, UnitStride: true, Extent: extent, base: "left-base",
		PackedStoreProvenance: PackedStoreProvenance{HasPackedStoreDeps: true, HasFrozenPackedStoreDeps: true},
	}
	rightFacts := OptimizationFacts{Exclusive: true, ReadOnly: true, Contiguous: true, Extent: extent, base: "right-base"}
	mixedFacts := OptimizationFacts{
		ReadOnly: true, Contiguous: true, UnitStride: false, Extent: otherExtent, base: "left-base",
		PackedStoreProvenance: PackedStoreProvenance{
			HasPackedStoreDeps: true, HasFrozenPackedStoreDeps: true,
			HasNonFrozenPackedStoreDeps: true, HasNonStoreProvenance: true,
		},
	}
	root := &Result{ExprFacts: map[ast.Expr]OptimizationFacts{canonical: leftFacts, foreign: rightFacts}}
	privateNilMap := &Result{specializationRoot: root}
	privateEmptyMap := &Result{ExprFacts: map[ast.Expr]OptimizationFacts{}, specializationRoot: root}
	privateFacts := &Result{
		ExprFacts:           map[ast.Expr]OptimizationFacts{left: leftFacts, right: rightFacts, mixed: mixedFacts, private: leftFacts},
		specializationRoot:  root,
		specializationNodes: map[ast.Node]ast.Node{canonical: private},
		specializationOwned: map[ast.Node]bool{private: true, ownedMiss: true},
	}
	nilMap := &Result{}
	emptyMap := &Result{ExprFacts: map[ast.Expr]OptimizationFacts{}}
	plainFacts := &Result{ExprFacts: map[ast.Expr]OptimizationFacts{left: leftFacts, right: rightFacts, mixed: mixedFacts}}
	queryNames := []string{
		"ExprOptimizationFacts", "ExprsHaveSameExtent", "ExprsHaveEqualExtentSize", "ExprsAreDisjoint",
		"ExprSupportsDenseWrite", "ExprPackedStoreProvenance", "ExprHasPackedStoreProvenance",
		"ExprDependsOnFrozenPackedStores", "ExprDependsOnNonFrozenPackedStores",
		"ExprHasOnlyFrozenPackedStoreDeps", "ExprHasMixedPackedStoreProvenance",
		"ExprDependsOnlyOnFrozenPackedStores",
	}
	pairQueries := map[string]bool{
		"ExprsHaveSameExtent": true, "ExprsHaveEqualExtentSize": true, "ExprsAreDisjoint": true,
	}
	expressions := []ast.Expr{nil, left, right, mixed, missing, canonical, private, foreign, ownedMiss}
	cases := []struct {
		name   string
		result *Result
		view   CodegenReadView
	}{
		{name: "nil-result", view: NewCodegenReadView(nil, nil)},
		{name: "nil-map", result: nilMap, view: NewCodegenReadView(nilMap, nil)},
		{name: "empty-map", result: emptyMap, view: NewCodegenReadView(emptyMap, nil)},
		{name: "private-nil-map-does-not-inherit", result: privateNilMap, view: NewCodegenReadView(privateNilMap, nil)},
		{name: "private-empty-map-inherits-foreign-only", result: privateEmptyMap, view: NewCodegenReadView(privateEmptyMap, nil)},
		{name: "private-owned-and-inherited-rows", result: privateFacts, view: NewCodegenReadView(privateFacts, nil)},
		{name: "plain-populated", result: plainFacts, view: NewCodegenReadView(plainFacts, nil)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resultValue := reflect.ValueOf(testCase.result)
			viewValue := reflect.ValueOf(testCase.view)
			for _, name := range queryNames {
				if pairQueries[name] {
					for _, leftExpr := range expressions {
						for _, rightExpr := range expressions {
							args := []reflect.Value{optimizationExprArgument(leftExpr), optimizationExprArgument(rightExpr)}
							result := callOptimizationQuery(t, resultValue, name, args)
							view := callOptimizationQuery(t, viewValue, name, args)
							if !reflect.DeepEqual(result, view) {
								t.Fatalf("%s differs for (%v, %v): Result=%v view=%v", name, leftExpr, rightExpr, result, view)
							}
						}
					}
					continue
				}
				for _, expr := range expressions {
					args := []reflect.Value{optimizationExprArgument(expr)}
					result := callOptimizationQuery(t, resultValue, name, args)
					view := callOptimizationQuery(t, viewValue, name, args)
					if !reflect.DeepEqual(result, view) {
						t.Fatalf("%s differs for %v: Result=%v view=%v", name, expr, result, view)
					}
				}
			}
		})
	}
	if !privateFacts.ExprsHaveSameExtent(left, right) || !privateFacts.ExprsHaveEqualExtentSize(left, right) || !privateFacts.ExprsAreDisjoint(left, right) {
		t.Fatal("positive extent/disjoint predicate fixture is invalid")
	}
	if privateFacts.ExprsHaveSameExtent(left, mixed) || privateFacts.ExprsHaveEqualExtentSize(left, mixed) || privateFacts.ExprsAreDisjoint(left, mixed) {
		t.Fatal("negative extent/disjoint predicate fixture is invalid")
	}
	if !privateFacts.ExprSupportsDenseWrite(left) || privateFacts.ExprSupportsDenseWrite(right) || privateFacts.ExprSupportsDenseWrite(mixed) {
		t.Fatal("positive/negative dense-write predicate fixture is invalid")
	}
	if !privateFacts.ExprHasOnlyFrozenPackedStoreDeps(left) || !privateFacts.ExprDependsOnlyOnFrozenPackedStores(left) || !privateFacts.ExprHasMixedPackedStoreProvenance(mixed) || privateFacts.ExprHasOnlyFrozenPackedStoreDeps(mixed) {
		t.Fatal("positive/negative packed-store predicate fixture is invalid")
	}
	if privateFacts.ExprHasPackedStoreProvenance(right) || privateFacts.ExprDependsOnFrozenPackedStores(right) || privateFacts.ExprDependsOnNonFrozenPackedStores(left) {
		t.Fatal("negative packed-store query fixture is invalid")
	}

	var events []CodegenFactRead
	observed := NewCodegenReadView(privateFacts, func(read CodegenFactRead) { events = append(events, read) })
	if _, ok := observed.ExprOptimizationFacts(nil); ok || len(events) != 0 {
		t.Fatal("nil expression performed or reported an optimization-fact read")
	}
	if facts, ok := observed.ExprOptimizationFacts(left); !ok || !reflect.DeepEqual(facts, leftFacts) {
		t.Fatal("view did not return the physical positive ExprFacts row")
	}
	if _, ok := observed.ExprOptimizationFacts(ownedMiss); ok {
		t.Fatal("view revived a canonical row for an owned miss")
	}
	if len(events) != 4 || events[0].Operation != "own-map-presence" || events[0].Result != privateFacts || !events[0].Present || events[1].Operation != "lookup" || events[1].Table != "ExprFacts" || events[1].Key != left || !events[1].Present || events[2].Operation != "own-map-presence" || events[2].Result != privateFacts || events[3].Operation != "lookup" || events[3].Table != "ExprFacts" || events[3].Key != ownedMiss || events[3].Present {
		t.Fatalf("optimization reads did not retain physical-presence, key, result, or negative membership: %+v", events)
	}
	events = nil
	if _, ok := NewCodegenReadView(privateNilMap, func(read CodegenFactRead) { events = append(events, read) }).ExprOptimizationFacts(foreign); ok || len(events) != 1 || events[0].Operation != "own-map-presence" || events[0].Present {
		t.Fatalf("nil physical ExprFacts map inherited a row or reported a key lookup: %+v", events)
	}
	events = nil
	if facts, ok := NewCodegenReadView(privateEmptyMap, func(read CodegenFactRead) { events = append(events, read) }).ExprOptimizationFacts(foreign); !ok || !reflect.DeepEqual(facts, rightFacts) || len(events) != 2 || events[0].Operation != "own-map-presence" || !events[0].Present || events[1].Operation != "lookup" || events[1].Result != privateEmptyMap || !events[1].Present {
		t.Fatalf("allocated empty map did not preserve canonical fallback and read context: facts=%+v events=%+v", facts, events)
	}
}

func optimizationExprArgument(expr ast.Expr) reflect.Value {
	if expr == nil {
		return reflect.Zero(reflect.TypeFor[ast.Expr]())
	}
	return reflect.ValueOf(expr)
}

func callOptimizationQuery(t *testing.T, receiver reflect.Value, name string, args []reflect.Value) []any {
	t.Helper()
	method := receiver.MethodByName(name)
	if !method.IsValid() {
		t.Fatalf("missing optimization query method %s", name)
	}
	values := method.Call(args)
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value.Interface()
	}
	return result
}

// Exercise every schema entry: physical map semantics must not become logical
// sparse inheritance, and observed membership must outlive caller map mutation.
func TestCodegenReadViewEveryOwnMapPreservesPhysicalMembershipSnapshot(t *testing.T) {
	for _, field := range codegenReadSchema(t) {
		if field.Analyzer == nil {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			root := &Result{}
			table := reflect.ValueOf(root).Elem().FieldByName(field.Name)
			key, value := reflect.Zero(table.Type().Key()), reflect.Zero(table.Type().Elem())
			table.Set(reflect.MakeMap(table.Type()))
			table.SetMapIndex(key, value)
			current := &Result{specializationRoot: root}
			currentTable := reflect.ValueOf(current).Elem().FieldByName(field.Name)
			var events []CodegenFactRead
			call := func(view CodegenReadView, prefix string) reflect.Value {
				return reflect.ValueOf(view).MethodByName(prefix + field.Name + "Map").Call(nil)[0]
			}
			view := NewCodegenReadView(current, func(read CodegenFactRead) { events = append(events, read) })
			// The base has a real logical row, while the physical private table is nil.
			logical := reflect.ValueOf(view).MethodByName("Lookup" + field.Name).Call([]reflect.Value{key})
			if !logical[1].Bool() {
				t.Fatal("fixture lost inherited logical row")
			}
			events = nil
			if call(view, "HasOwn").Bool() || !call(view, "Own").IsNil() {
				t.Fatal("physical view inherited canonical root map")
			}
			if len(events) != 2 || events[0].Present || events[1].Present || !reflect.ValueOf(events[1].Value).IsNil() {
				t.Fatal("nil physical map observation changed")
			}
			// Nil Result has the same physical absence with its own explicit context.
			events = nil
			nilView := NewCodegenReadView(nil, view.Observe)
			if call(nilView, "HasOwn").Bool() || !call(nilView, "Own").IsNil() {
				t.Fatal("nil Result physical map changed")
			}
			if events[0].Result != nil || events[1].Result != nil {
				t.Fatal("nil context not preserved")
			}
			currentTable.Set(reflect.MakeMap(table.Type()))
			events = nil
			if !call(view, "HasOwn").Bool() {
				t.Fatal("allocated empty map lost physical presence")
			}
			own := call(view, "Own")
			emptySnapshot := reflect.ValueOf(events[1].Value)
			if own.IsNil() || emptySnapshot.IsNil() || emptySnapshot.Len() != 0 || !events[1].Present {
				t.Fatal("allocated empty snapshot became nil")
			}
			own.SetMapIndex(key, value)
			if currentTable.Len() != 1 || emptySnapshot.Len() != 0 {
				t.Fatal("returned map not original or event aliased membership")
			}
			events = nil
			own = call(view, "Own")
			snapshot := reflect.ValueOf(events[0].Value)
			if snapshot.Len() != 1 || !snapshot.MapIndex(key).IsValid() {
				t.Fatal("stored nil/false row missing from snapshot")
			}
			own.SetMapIndex(key, reflect.Value{})
			if currentTable.Len() != 0 || snapshot.Len() != 1 {
				t.Fatal("map deletion mutated prior snapshot")
			}
			if events[0].Result != current || events[0].Table != field.Name || events[0].Operation != "own-map" {
				t.Fatal("map event lost physical Result/table/operation")
			}
			// Disabled tracing still returns the original physical map and emits nothing.
			disabled := NewCodegenReadView(current, nil)
			call(disabled, "Own").SetMapIndex(key, value)
			if currentTable.Len() != 1 || len(events) != 1 {
				t.Fatal("disabled physical read changed semantics")
			}
		})
	}
}

func TestCodegenReadViewOwnMapSnapshotLeavesBorrowedValuesOpaque(t *testing.T) {
	key := &ast.Ident{}
	typ := &BuiltinType{Name: "before"}
	root := &Result{ExprTypes: map[ast.Expr]Type{key: typ}}
	var read CodegenFactRead
	view := NewCodegenReadView(root, func(event CodegenFactRead) { read = event })
	own := view.OwnExprTypesMap()
	snapshot := read.Value.(map[ast.Expr]Type)
	typ.Name = "after"
	delete(own, key)
	if len(snapshot) != 1 || snapshot[key] != typ || snapshot[key].(*BuiltinType).Name != "after" {
		t.Fatal("snapshot must preserve membership while leaving borrowed nominal payload opaque")
	}
}
