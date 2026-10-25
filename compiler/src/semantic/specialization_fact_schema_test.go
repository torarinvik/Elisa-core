//go:build cgo

package semantic

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type unreviewedSparseFact struct{ Program map[string]Type }
type unreviewedSparseType struct{}

func (*unreviewedSparseType) String() string { return "unreviewed" }
func (*unreviewedSparseType) isType()        {}

type unreviewedSparseShape struct{}

func (*unreviewedSparseShape) String() string { return "unreviewed" }
func (*unreviewedSparseShape) isShape()       {}

func TestSparseFactSchemaMatchesReviewedMetadata(t *testing.T) {
	if err := validateSparseFactSchema(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{&FuncType{}, OptimizationFacts{}, &FunctionAnalysis{}, &Symbol{}, &GenericInstanceType{}, []Type{&BuiltinType{Name: "i64"}}, map[string]regionRefState{}} {
		if err := validateSparseFactValueType(reflect.ValueOf(value)); err != nil {
			t.Fatalf("reviewed %T rejected: %v", value, err)
		}
	}
}

func TestSparseFactSchemaRejectsConstructionResourcesAndUnknownVariants(t *testing.T) {
	values := []any{(*Analyzer)(nil), Analyzer{}, (*RetainedAnalysis)(nil), (*parallelWorkerState)(nil), (*specializationContext)(nil), (*sharedCodegen)(nil), []*Analyzer{}, map[string]*RetainedAnalysis{}, unreviewedSparseFact{}, &unreviewedSparseType{}, &unreviewedSparseShape{}, func() {}, make(chan int), uintptr(0)}
	for _, value := range values {
		if err := validateSparseFactValueType(reflect.ValueOf(value)); err == nil {
			t.Fatalf("resource/unreviewed %T admitted", value)
		}
	}
	var dynamic Type = &unreviewedSparseType{}
	boxed := reflect.ValueOf(&dynamic).Elem()
	// Traversal checks both the interface envelope and its concrete value.
	if err := validateSparseFactValueType(boxed); err != nil {
		t.Fatal("reviewed Type interface envelope rejected", err)
	}
	if err := validateSparseFactValueType(boxed.Elem()); err == nil || !strings.Contains(err.Error(), "unsupported Type variant") {
		t.Fatalf("unknown dynamic Type admitted: %v", err)
	}
}

func TestSparseFactSchemaDetectsNestedFieldDrift(t *testing.T) {
	type fixture struct {
		Field string `json:"field"`
	}
	typ := reflect.TypeFor[fixture]()
	valid := sparseFactSchema{kind: reflect.Struct, fields: []sparseFactField{{name: "Field", typ: reflect.TypeFor[string](), tag: `json:"field"`}}}
	if err := checkSparseFactDescriptor(typ, valid); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []sparseFactSchema{
		{kind: reflect.Struct},
		{kind: reflect.Struct, fields: []sparseFactField{{name: "Field", typ: reflect.TypeFor[int](), tag: `json:"field"`}}},
		{kind: reflect.Struct, fields: []sparseFactField{{name: "Field", typ: reflect.TypeFor[string](), tag: `json:"other"`}}},
		{kind: reflect.Struct, fields: []sparseFactField{{name: "Other", typ: reflect.TypeFor[string](), tag: `json:"field"`}}},
	} {
		if err := checkSparseFactDescriptor(typ, changed); err == nil {
			t.Fatal("nested field addition/type/name/tag drift admitted")
		}
	}
}

// Reflection cannot enumerate all implementors of a sealed interface. Keep the
// production source census closed as well as rejecting unknown dynamic values.
func TestSparseFactSchemaTypeAndShapeVariantCensus(t *testing.T) {
	expected := map[string]map[string]bool{"isType": {}, "isShape": {}}
	for typ := range sparseFactKnownTypes {
		expected["isType"][typ.Elem().Name()] = true
	}
	for typ := range sparseFactKnownShapes {
		expected["isShape"][typ.Elem().Name()] = true
	}
	seen := map[string]map[string]bool{"isType": {}, "isShape": {}}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source path unavailable")
	}
	entries, err := os.ReadDir(filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(source), name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok || fn.Recv == nil || expected[fn.Name.Name] == nil {
				continue
			}
			receiver := fn.Recv.List[0].Type
			if pointer, ok := receiver.(*goast.StarExpr); ok {
				receiver = pointer.X
			}
			ident, ok := receiver.(*goast.Ident)
			if !ok {
				t.Fatalf("unreviewed %s receiver syntax in %s", fn.Name.Name, name)
			}
			if !expected[fn.Name.Name][ident.Name] {
				t.Errorf("new %s implementation %s needs sparse-fact ownership review", fn.Name.Name, ident.Name)
			}
			seen[fn.Name.Name][ident.Name] = true
		}
	}
	for method, kinds := range expected {
		for kind := range kinds {
			if !seen[method][kind] {
				t.Errorf("reviewed %s kind %s no longer matches source", method, kind)
			}
		}
	}
}
