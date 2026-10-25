package semantic

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

type sparseFactField struct {
	name      string
	typ       reflect.Type
	anonymous bool
	tag       string
}
type sparseFactMethod struct {
	name string
	typ  reflect.Type
}
type sparseFactSchema struct {
	kind      reflect.Kind
	fields    []sparseFactField
	key, elem reflect.Type
	length    int
	methods   []sparseFactMethod
}

var sparseFactSemanticPackage = reflect.TypeFor[Symbol]().PkgPath()
var sparseFactTypeInterface = reflect.TypeFor[Type]()
var sparseFactShapeInterface = reflect.TypeFor[Shape]()
var sparseFactTypeChecks sync.Map // reflect.Type -> immutable validation outcome

type sparseFactTypeCheck struct{ err error }

// Validate the fixed source-reviewed schemas once before publication. This is
// not generated on build: new fields/types require an ownership/memory review.
func validateSparseFactSchema() error { return sparseFactSchemaValidation() }

var sparseFactSchemaValidation = sync.OnceValue(func() error {
	types := make([]reflect.Type, 0, len(sparseFactSchemas))
	for typ := range sparseFactSchemas {
		types = append(types, typ)
	}
	sort.Slice(types, func(i, j int) bool { return types[i].String() < types[j].String() })
	for _, typ := range types {
		schema := sparseFactSchemas[typ]
		if err := checkSparseFactDescriptor(typ, schema); err != nil {
			return err
		}
	}
	for _, typ := range types {
		if err := validateSparseFactType(typ, map[reflect.Type]bool{}); err != nil {
			return err
		}
	}
	return nil
})

func checkSparseFactDescriptor(typ reflect.Type, schema sparseFactSchema) error {
	if typ.Kind() != schema.kind {
		return fmt.Errorf("sparse fact schema kind changed for %s: got %s want %s", typ, typ.Kind(), schema.kind)
	}
	switch schema.kind {
	case reflect.Struct:
		if typ.NumField() != len(schema.fields) {
			return fmt.Errorf("sparse fact schema fields changed for %s: got %d want %d", typ, typ.NumField(), len(schema.fields))
		}
		for i, expected := range schema.fields {
			field := typ.Field(i)
			if field.Name != expected.name || field.Type != expected.typ || field.Anonymous != expected.anonymous || string(field.Tag) != expected.tag {
				return fmt.Errorf("sparse fact schema field changed for %s at %d (%s)", typ, i, expected.name)
			}
		}
	case reflect.Map:
		if typ.Key() != schema.key || typ.Elem() != schema.elem {
			return fmt.Errorf("sparse fact map schema changed for %s", typ)
		}
	case reflect.Pointer, reflect.Slice:
		if typ.Elem() != schema.elem {
			return fmt.Errorf("sparse fact element schema changed for %s", typ)
		}
	case reflect.Array:
		if typ.Elem() != schema.elem || typ.Len() != schema.length {
			return fmt.Errorf("sparse fact array schema changed for %s", typ)
		}
	case reflect.Interface:
		if typ.NumMethod() != len(schema.methods) {
			return fmt.Errorf("sparse fact interface methods changed for %s", typ)
		}
		for _, expected := range schema.methods {
			method, ok := typ.MethodByName(expected.name)
			if !ok || method.Type != expected.typ {
				return fmt.Errorf("sparse fact interface method changed for %s.%s", typ, expected.name)
			}
		}
	}
	return nil
}

// Called for every value BEFORE borrowed-root/AST handling. The caller still
// enforces canonical object identity and owns AST traversal; this function
// validates closed semantic structure and rejects construction/resource types.
func validateSparseFactValueType(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}
	if err := validateSparseFactSchema(); err != nil {
		return err
	}
	typ := value.Type()
	if cached, ok := sparseFactTypeChecks.Load(typ); ok {
		return cached.(sparseFactTypeCheck).err
	}
	err := validateSparseFactType(typ, map[reflect.Type]bool{})
	sparseFactTypeChecks.Store(typ, sparseFactTypeCheck{err: err})
	return err
}

func validateSparseFactType(typ reflect.Type, seen map[reflect.Type]bool) error {
	if typ == nil {
		return nil
	}
	base := typ
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.PkgPath() == sparseFactSemanticPackage {
		switch base.Name() {
		case "Analyzer", "RetainedAnalysis", "parallelWorkerState", "specializationContext", "sharedCodegen", "specializationPublication", "specializationQueries", "specializationSyntax", "analyzerExprTypeOverlay", "analyzerExprFactsOverlay":
			return fmt.Errorf("sparse fact retains construction/resource type %s", typ)
		}
	}
	if typ.Kind() != reflect.Interface {
		if typ.Implements(sparseFactTypeInterface) && !sparseFactKnownTypes[typ] {
			return fmt.Errorf("sparse fact has unsupported Type variant %s", typ)
		}
		if typ.Implements(sparseFactShapeInterface) && !sparseFactKnownShapes[typ] {
			return fmt.Errorf("sparse fact has unsupported Shape variant %s", typ)
		}
	}
	if sparseFactBorrowedTypes[typ] {
		return nil
	}
	// AST shapes are a separately reviewed owned-syntax boundary. Their fields
	// remain traversed by publication.walk, so a nested resource value is still
	// checked. This table does not claim a closed grammar for every AST variant.
	if typ.PkgPath() == astGraphPkgPath {
		return nil
	}
	if seen[typ] {
		return nil
	}
	seen[typ] = true
	if schema, known := sparseFactSchemas[typ]; known {
		if err := checkSparseFactDescriptor(typ, schema); err != nil {
			return err
		}
		switch schema.kind {
		case reflect.Struct:
			for _, field := range schema.fields {
				if err := validateSparseFactType(field.typ, seen); err != nil {
					return fmt.Errorf("%s.%s: %w", typ, field.name, err)
				}
			}
		case reflect.Map:
			if err := validateSparseFactType(schema.key, seen); err != nil {
				return err
			}
			return validateSparseFactType(schema.elem, seen)
		case reflect.Pointer, reflect.Slice, reflect.Array:
			return validateSparseFactType(schema.elem, seen)
		}
		return nil
	}
	if typ.Name() != "" && typ.PkgPath() != "" {
		return fmt.Errorf("sparse fact has unreviewed named type %s", typ)
	}
	switch typ.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return nil
	case reflect.Interface:
		// Empty interfaces require dynamic-value checks at each traversal step.
		if typ.NumMethod() == 0 {
			return nil
		}
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return validateSparseFactType(typ.Elem(), seen)
	case reflect.Map:
		if err := validateSparseFactType(typ.Key(), seen); err != nil {
			return err
		}
		return validateSparseFactType(typ.Elem(), seen)
	}
	return fmt.Errorf("sparse fact has unsupported value type %s", typ)
}
