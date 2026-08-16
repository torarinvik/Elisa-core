package frontendir

// The `.elisair` schema: an explicit, versioned description of the frontend IR's
// shape, independent of Go's type graph.
//
// The v1 bundle was `encoding/gob` over the live `ast` types. That made the file
// format an accident of stage0's Go declarations — unreadable by any other
// implementation, silently different whenever a field was added, and (see
// TestGobDropsNodeIdentity) unable to express the one thing the AST actually
// needs: NODE IDENTITY. `ast.File.DeclVisibility` is keyed by the decl pointer,
// and gob writes each key as a fresh value, so every lookup missed after a
// round-trip and `public:` marks were lost.
//
// v2 describes the tree with numeric type and field IDs carried IN the file. Two
// properties follow, and both are the point of the exercise:
//
//   - Language neutral. A reader needs the ID registry, not Go. Every value is
//     length-delimited and every node is in a flat table, so an unknown type or
//     field can be SKIPPED rather than being a parse failure. An implementation
//     that models half the AST can still read, walk and rewrite the whole file.
//   - Identity preserving. Nodes live in a table and are referenced by index, so
//     shared and cyclic references survive exactly. Maps keyed by a node are
//     ordinary (ref, value) pairs.
//
// IDs come from schema_registry.txt, which is COMMITTED and append-only. The
// structure is still derived from the Go types by reflection — that keeps the two
// from drifting apart by hand — but the numbering is fixed on disk, and
// TestSchemaMatchesRegistry fails if a field changes ID or vanishes. Renaming a Go
// field is therefore a deliberate, reviewable schema change.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// SchemaVersion is the wire format's version. Bump only for a change a v2 reader
// cannot absorb; adding a type or field is backward compatible by construction.
const SchemaVersion = 2

// Magic prefixes every v2 bundle.
var Magic = []byte("ELISAIR2\n")

// WireKind is how a field's value is laid out. Every value is length-delimited,
// so a reader that does not recognise a kind can still skip it.
type WireKind uint8

const (
	WireBool   WireKind = 1 // one byte, 0 or 1
	WireInt    WireKind = 2 // zigzag varint (covers int, int64, and named int types)
	WireString WireKind = 3 // raw UTF-8, length from the field header
	WireNode   WireKind = 4 // uvarint node index; 0 means null
	WireList   WireKind = 5 // uvarint count, then that many encoded elements
	WireMap    WireKind = 6 // uvarint count, then count (key, value) pairs
	WireStruct WireKind = 7 // an inline (non-pointer) struct: uvarint typeID, then its fields
)

func (k WireKind) String() string {
	switch k {
	case WireBool:
		return "bool"
	case WireInt:
		return "int"
	case WireString:
		return "string"
	case WireNode:
		return "node"
	case WireList:
		return "list"
	case WireMap:
		return "map"
	case WireStruct:
		return "struct"
	}
	return fmt.Sprintf("kind%d", uint8(k))
}

// FieldSchema describes one field of one type.
type FieldSchema struct {
	ID   uint32
	Name string
	Kind WireKind
	// Elem describes a list's element or a map's value; Key describes a map's key.
	// Nil for scalar kinds.
	Elem *ValueSchema
	Key  *ValueSchema

	goIndex int // index into the Go struct's fields; not part of the wire format
}

// ValueSchema describes a nested value (a list element, a map key or value).
type ValueSchema struct {
	Kind WireKind
	Elem *ValueSchema
	Key  *ValueSchema
	// StructType names the inline struct type for WireStruct, so a reader can
	// resolve it in the type table.
	StructType string
}

// TypeSchema describes one node or inline struct type.
type TypeSchema struct {
	ID     uint32
	Name   string
	Fields []FieldSchema

	goType reflect.Type
}

// Schema is the full description carried by a bundle.
type Schema struct {
	Types  []TypeSchema
	byName map[string]*TypeSchema
	byID   map[uint32]*TypeSchema
	byGo   map[reflect.Type]*TypeSchema
}

// TypeByName resolves a type descriptor by its schema name.
func (s *Schema) TypeByName(name string) (*TypeSchema, bool) {
	t, ok := s.byName[name]
	return t, ok
}

// TypeByID resolves a type descriptor by its numeric ID.
func (s *Schema) TypeByID(id uint32) (*TypeSchema, bool) {
	t, ok := s.byID[id]
	return t, ok
}

// typeForGo resolves the descriptor for a live Go type.
func (s *Schema) typeForGo(rt reflect.Type) (*TypeSchema, bool) {
	t, ok := s.byGo[rt]
	return t, ok
}

// Field resolves a field descriptor by ID.
func (t *TypeSchema) Field(id uint32) (*FieldSchema, bool) {
	for i := range t.Fields {
		if t.Fields[i].ID == id {
			return &t.Fields[i], true
		}
	}
	return nil, false
}

// schemaName is the name a Go type carries in the schema. It is package-qualified
// only where it must be, so the common `ast` types read plainly.
func schemaName(rt reflect.Type) string {
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	pkg := rt.PkgPath()
	switch {
	case strings.HasSuffix(pkg, "/src/ast"):
		return rt.Name()
	case pkg == "":
		return rt.Name()
	default:
		return pkg[strings.LastIndex(pkg, "/")+1:] + "." + rt.Name()
	}
}

// deriveSchema walks the Go type graph from the bundle root and produces a
// structural description of every reachable node and inline struct type. The
// numbering here is provisional and sorted for determinism; applyRegistry
// replaces it with the committed IDs.
func deriveSchema(roots []reflect.Type) (*Schema, error) {
	found := map[reflect.Type]bool{}
	var order []reflect.Type

	var visitType func(rt reflect.Type) error
	var visitValue func(rt reflect.Type) (*ValueSchema, error)

	// valueSchemaFor classifies a field or element type, recording any struct
	// types it reaches.
	visitValue = func(rt reflect.Type) (*ValueSchema, error) {
		switch rt.Kind() {
		case reflect.Bool:
			return &ValueSchema{Kind: WireBool}, nil
		case reflect.String:
			return &ValueSchema{Kind: WireString}, nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return &ValueSchema{Kind: WireInt}, nil
		case reflect.Interface:
			// A node reference: the concrete type is recorded per node in the table.
			return &ValueSchema{Kind: WireNode}, nil
		case reflect.Ptr:
			if rt.Elem().Kind() != reflect.Struct {
				return nil, fmt.Errorf("unsupported pointer type %s", rt)
			}
			if err := visitType(rt.Elem()); err != nil {
				return nil, err
			}
			return &ValueSchema{Kind: WireNode}, nil
		case reflect.Slice:
			elem, err := visitValue(rt.Elem())
			if err != nil {
				return nil, fmt.Errorf("slice %s: %w", rt, err)
			}
			return &ValueSchema{Kind: WireList, Elem: elem}, nil
		case reflect.Map:
			key, err := visitValue(rt.Key())
			if err != nil {
				return nil, fmt.Errorf("map key %s: %w", rt, err)
			}
			val, err := visitValue(rt.Elem())
			if err != nil {
				return nil, fmt.Errorf("map value %s: %w", rt, err)
			}
			return &ValueSchema{Kind: WireMap, Key: key, Elem: val}, nil
		case reflect.Struct:
			// An inline struct value (lexer.Pos, ParamDecl, ...). It gets a type
			// descriptor of its own but does not live in the node table.
			if err := visitType(rt); err != nil {
				return nil, err
			}
			return &ValueSchema{Kind: WireStruct, StructType: schemaName(rt)}, nil
		default:
			return nil, fmt.Errorf("unsupported kind %s (%s)", rt.Kind(), rt)
		}
	}

	visitType = func(rt reflect.Type) error {
		if rt.Kind() == reflect.Ptr {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || found[rt] {
			return nil
		}
		found[rt] = true
		order = append(order, rt)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" { // unexported: not part of the format
				continue
			}
			if _, err := visitValue(f.Type); err != nil {
				return fmt.Errorf("%s.%s: %w", schemaName(rt), f.Name, err)
			}
		}
		return nil
	}

	for _, root := range roots {
		if err := visitType(root); err != nil {
			return nil, err
		}
	}

	schema := &Schema{
		byName: map[string]*TypeSchema{},
		byID:   map[uint32]*TypeSchema{},
		byGo:   map[reflect.Type]*TypeSchema{},
	}
	sort.Slice(order, func(i, j int) bool { return schemaName(order[i]) < schemaName(order[j]) })
	for _, rt := range order {
		ts := TypeSchema{Name: schemaName(rt), goType: rt}
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue
			}
			vs, err := visitValue(f.Type)
			if err != nil {
				return nil, err
			}
			ts.Fields = append(ts.Fields, FieldSchema{
				Name:    f.Name,
				Kind:    vs.Kind,
				Elem:    vs.Elem,
				Key:     vs.Key,
				goIndex: i,
			})
		}
		sort.Slice(ts.Fields, func(i, j int) bool { return ts.Fields[i].Name < ts.Fields[j].Name })
		schema.Types = append(schema.Types, ts)
	}
	for i := range schema.Types {
		t := &schema.Types[i]
		schema.byName[t.Name] = t
		schema.byGo[t.goType] = t
	}
	return schema, nil
}
