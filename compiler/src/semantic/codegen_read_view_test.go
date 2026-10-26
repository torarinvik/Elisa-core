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
