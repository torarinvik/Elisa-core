package semantic

import (
	"reflect"
	"strconv"
	"testing"

	"elisacore/src/ast"
)

type reflectMetaInner struct{ Value int }

type reflectMetaOuter struct {
	reflectMetaInner
	Name   string
	hidden bool
}

// More types than front-cache slots: colliding types must still get their own metadata.
func TestReflectFieldMetaFrontCacheCollisions(t *testing.T) {
	var types []reflect.Type
	for i := 0; i < 600; i++ {
		types = append(types, reflect.StructOf([]reflect.StructField{
			{Name: "F" + strconv.Itoa(i), Type: reflect.TypeOf(0)},
			{Name: "Value", Type: reflect.TypeOf("")},
		}))
	}
	for round := 0; round < 3; round++ {
		for i, typ := range types {
			fs := reflectFields(typ)
			if len(fs) != 2 || fs[0].Name != "F"+strconv.Itoa(i) {
				t.Fatalf("type %d round %d: wrong metadata %v", i, round, fs)
			}
			if idx, ok := reflectFieldIndex(typ, "F"+strconv.Itoa(i)); !ok || len(idx) != 1 || idx[0] != 0 {
				t.Fatalf("type %d: wrong index %v %v", i, idx, ok)
			}
		}
	}
}

// The cached metadata is exactly what reflect returns, for AST node types and for promoted and
// missing fields, and it is computed once per type.
func TestReflectFieldMetaMatchesReflect(t *testing.T) {
	samples := []any{ast.VarDeclStmt{}, ast.AssignStmt{}, ast.ForStmt{}, ast.CallExpr{}, ast.MatchStmt{}, reflectMetaOuter{}}
	for _, sample := range samples {
		typ := reflect.TypeOf(sample)
		fs := reflectFields(typ)
		if len(fs) != typ.NumField() {
			t.Fatalf("%v: %d cached fields, want %d", typ, len(fs), typ.NumField())
		}
		for i := range fs {
			if !reflect.DeepEqual(fs[i], typ.Field(i)) {
				t.Fatalf("%v field %d: cached %+v, reflect %+v", typ, i, fs[i], typ.Field(i))
			}
		}
		if again := reflectFields(typ); len(fs) > 0 && &again[0] != &fs[0] {
			t.Fatalf("%v: metadata must be computed once and shared", typ)
		}
		v := reflect.New(typ).Elem()
		for _, name := range []string{"Name", "Value", "Target", "Store", "Targets", "NoSuchField", "hidden"} {
			got, want := reflectFieldByName(v, name), v.FieldByName(name)
			if got.IsValid() != want.IsValid() || got.IsValid() && (got.Type() != want.Type() || got.UnsafeAddr() != want.UnsafeAddr()) {
				t.Fatalf("%v.%s: cached lookup disagrees with FieldByName", typ, name)
			}
			_, has := reflectFieldIndex(typ, name)
			if _, want := typ.FieldByName(name); has != want {
				t.Fatalf("%v.%s: presence %v, want %v", typ, name, has, want)
			}
		}
	}
}
