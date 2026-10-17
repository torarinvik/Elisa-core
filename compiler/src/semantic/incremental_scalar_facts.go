package semantic

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"elisacore/src/ast"
)

// FunctionScalarObservations is one explicitly bounded portion of a function
// product. Callers must supply this function's contributions only. It is not a
// complete Result, and cannot authorize skipping analysis on its own.
type FunctionScalarObservations struct {
	ExprTypes          map[ast.Expr]Type
	ResolvedTypeNames  map[ast.TypeExpr]string
	ResolvedValueNames map[*ast.Ident]string
	PermGrowthOps      map[ast.Expr]bool
	RewriteDefaults    map[*ast.Ident]bool
	IndexBoundsProven  map[*ast.IndexExpr]bool
	LemmaCalls         map[*ast.CallExpr]bool
	GhostDecls         map[*ast.VarDeclStmt]bool
	GhostContracts     map[ast.Expr]bool
}

// FunctionScalarFacts owns only encoded values. No AST, semantic Type, Scope or
// Analyzer object remains reachable through a captured product.
type FunctionScalarFacts struct{ encoded string }

func (facts FunctionScalarFacts) Encoded() string { return facts.encoded }

const functionScalarFactsVersion = 1
const functionScalarFactsMaxBytes = 4 * 1024 * 1024
const functionScalarFactsMaxEntries = 100_000

var scalarObservationSchema = map[string]reflect.Type{
	"ExprTypes":          reflect.TypeOf(map[ast.Expr]Type(nil)),
	"ResolvedTypeNames":  reflect.TypeOf(map[ast.TypeExpr]string(nil)),
	"ResolvedValueNames": reflect.TypeOf(map[*ast.Ident]string(nil)),
	"PermGrowthOps":      reflect.TypeOf(map[ast.Expr]bool(nil)),
	"RewriteDefaults":    reflect.TypeOf(map[*ast.Ident]bool(nil)),
	"IndexBoundsProven":  reflect.TypeOf(map[*ast.IndexExpr]bool(nil)),
	"LemmaCalls":         reflect.TypeOf(map[*ast.CallExpr]bool(nil)),
	"GhostDecls":         reflect.TypeOf(map[*ast.VarDeclStmt]bool(nil)),
	"GhostContracts":     reflect.TypeOf(map[ast.Expr]bool(nil)),
}

func validateScalarObservationSchema(typ reflect.Type) error {
	if typ.Kind() != reflect.Struct || typ.NumField() != len(scalarObservationSchema) {
		return fmt.Errorf("scalar observation schema changed")
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if expected, ok := scalarObservationSchema[field.Name]; !ok || field.Type != expected {
			return fmt.Errorf("scalar observation schema changed: %s", field.Name)
		}
		resultField, ok := reflect.TypeOf(Result{}).FieldByName(field.Name)
		if !ok || resultField.Type != field.Type {
			return fmt.Errorf("scalar result field changed: %s", field.Name)
		}
	}
	return nil
}

type scalarTypeValue struct {
	Kind   string
	Name   string
	Signed bool
	Width  int
}
type scalarFactEntry struct {
	Field  string
	Anchor scalarNodeReference
	Type   *scalarTypeValue `json:",omitempty"`
	Text   string           `json:",omitempty"`
	Flag   bool             `json:",omitempty"`
}
type scalarNodeReference struct {
	Ordinal uint32
	Kind    string
}
type scalarFactsPayload struct {
	Version     int
	FunctionKey string
	Tree        [32]byte
	Entries     []scalarFactEntry
}

// Audited leaf types only. Numeric CanonicalTypeID values are invocation-local,
// and Type.String() does not encode all semantic properties of arbitrary types.
func captureScalarType(t Type) (*scalarTypeValue, error) {
	value := reflect.ValueOf(t)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return nil, fmt.Errorf("missing scalar expression type")
	}
	fields := 0
	var out scalarTypeValue
	switch t := t.(type) {
	case *BuiltinType:
		fields = 1
		out = scalarTypeValue{Kind: "builtin", Name: t.Name}
	case *BitIntType:
		fields = 2
		out = scalarTypeValue{Kind: "bitint", Signed: t.Signed, Width: t.Width}
	case *NeverType:
		out.Kind = "never"
	case *NullType:
		out.Kind = "null"
	default:
		return nil, fmt.Errorf("unsupported function scalar type %T", t)
	}
	if value.Elem().NumField() != fields {
		return nil, fmt.Errorf("scalar type schema changed: %T", t)
	}
	if _, err := restoreScalarType(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
func restoreScalarType(t *scalarTypeValue) (Type, error) {
	if t == nil {
		return nil, fmt.Errorf("missing scalar type value")
	}
	switch t.Kind {
	case "builtin":
		if t.Width != 0 || t.Signed {
			break
		}
		switch t.Name {
		case "void", "bool", "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64", "Local", "Frozen", "Joinable", "Pending", "Held":
			return &BuiltinType{Name: t.Name}, nil
		}
	case "bitint":
		if t.Name == "" && t.Width >= 1 && t.Width <= 64 {
			return &BitIntType{Signed: t.Signed, Width: t.Width}, nil
		}
	case "never":
		if t.Name == "" && t.Width == 0 && !t.Signed {
			return &NeverType{}, nil
		}
	case "null":
		if t.Name == "" && t.Width == 0 && !t.Signed {
			return &NullType{}, nil
		}
	}
	return nil, fmt.Errorf("invalid scalar type value")
}

func CaptureFunctionScalarFacts(index *FunctionSourceIndex, input FunctionScalarObservations) (FunctionScalarFacts, error) {
	if index == nil {
		return FunctionScalarFacts{}, fmt.Errorf("missing function source index")
	}
	// This reflection traversal is deliberately gated: adding an observation field
	// requires its payload/restore policy to be implemented below as well.
	if err := validateScalarObservationSchema(reflect.TypeOf(input)); err != nil {
		return FunctionScalarFacts{}, err
	}
	var entries []scalarFactEntry
	var nodes []PositionedFunctionNode
	// JSON escaping costs at most six bytes per input byte. Reserve enough for
	// fixed fields, node kind and punctuation. Function identity and the source
	// digest are stored once per product, rather than repeated on every fact.
	// This bound is checked before append/Marshal; false misses are harmless.
	encodedBudget := 512 + 6*len(index.key)
	appendEntry := func(field string, node PositionedFunctionNode, typ Type, text string, flag bool) error {
		if len(entries) >= functionScalarFactsMaxEntries {
			return fmt.Errorf("scalar fact entry budget exceeded")
		}
		entry := scalarFactEntry{Field: field, Text: text, Flag: flag}
		if field == "ExprTypes" {
			value, err := captureScalarType(typ)
			if err != nil {
				return err
			}
			entry.Type = value
		}
		cost := len(text) + len(field) + len(reflect.TypeOf(node).String())
		if entry.Type != nil {
			cost += len(entry.Type.Name)
		}
		if functionScalarFactsMaxBytes-encodedBudget < 128 || cost > (functionScalarFactsMaxBytes-encodedBudget-128)/6 {
			return fmt.Errorf("scalar fact string budget exceeded")
		}
		encodedBudget += 128 + 6*cost
		entries = append(entries, entry)
		nodes = append(nodes, node)
		return nil
	}
	value := reflect.ValueOf(input)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i).Name
		iter := value.Field(i).MapRange()
		for iter.Next() {
			node, ok := iter.Key().Interface().(PositionedFunctionNode)
			if !ok {
				return FunctionScalarFacts{}, fmt.Errorf("scalar fact key lacks source position")
			}
			var typ Type
			var text string
			var flag bool
			switch field {
			case "ExprTypes":
				typ, _ = iter.Value().Interface().(Type)
			case "ResolvedTypeNames", "ResolvedValueNames":
				text = iter.Value().String()
			default:
				flag = iter.Value().Bool()
			}
			if err := appendEntry(field, node, typ, text, flag); err != nil {
				return FunctionScalarFacts{}, err
			}
		}
	}
	anchors, err := index.Capture(nodes)
	if err != nil {
		return FunctionScalarFacts{}, err
	}
	for i := range entries {
		entries[i].Anchor = scalarNodeReference{anchors[i].Ordinal, anchors[i].Kind}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Field != entries[j].Field {
			return entries[i].Field < entries[j].Field
		}
		return entries[i].Anchor.Ordinal < entries[j].Anchor.Ordinal
	})
	data, err := json.Marshal(scalarFactsPayload{Version: functionScalarFactsVersion, FunctionKey: index.key, Tree: index.digest, Entries: entries})
	if err != nil {
		return FunctionScalarFacts{}, err
	}
	if len(data) > functionScalarFactsMaxBytes {
		return FunctionScalarFacts{}, fmt.Errorf("scalar fact byte budget exceeded")
	}
	return FunctionScalarFacts{string(data)}, nil
}

// DecodeFunctionScalarFacts validates storage syntax. Restore validates source
// authority and typed node compatibility before returning any contribution.
// The enclosing query must validate semantic inputs, compiler/options and proof
// retry policy before calling Restore; checksums or anchors are not authority.
func DecodeFunctionScalarFacts(encoded string) (FunctionScalarFacts, error) {
	if _, err := decodeScalarFacts(encoded); err != nil {
		return FunctionScalarFacts{}, err
	}
	return FunctionScalarFacts{encoded}, nil
}
func decodeScalarFacts(encoded string) (*scalarFactsPayload, error) {
	if len(encoded) > functionScalarFactsMaxBytes {
		return nil, fmt.Errorf("scalar fact byte budget exceeded")
	}
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var payload scalarFactsPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("trailing scalar fact data")
	}
	if payload.Version != functionScalarFactsVersion || payload.FunctionKey == "" || len(payload.Entries) > functionScalarFactsMaxEntries {
		return nil, fmt.Errorf("unsupported scalar fact payload")
	}
	seen := make(map[string]bool, len(payload.Entries))
	for _, entry := range payload.Entries {
		key := fmt.Sprintf("%s:%d", entry.Field, entry.Anchor.Ordinal)
		if seen[key] {
			return nil, fmt.Errorf("duplicate scalar fact")
		}
		seen[key] = true
		switch entry.Field {
		case "ExprTypes":
			if entry.Text != "" || entry.Flag {
				return nil, fmt.Errorf("invalid expression type fact")
			}
			if _, err := restoreScalarType(entry.Type); err != nil {
				return nil, err
			}
		case "ResolvedTypeNames", "ResolvedValueNames":
			if entry.Type != nil || entry.Flag {
				return nil, fmt.Errorf("invalid resolved name fact")
			}
		case "PermGrowthOps", "RewriteDefaults", "IndexBoundsProven", "LemmaCalls", "GhostDecls", "GhostContracts":
			if entry.Type != nil || entry.Text != "" {
				return nil, fmt.Errorf("invalid boolean fact")
			}
		default:
			return nil, fmt.Errorf("unsupported scalar fact field %q", entry.Field)
		}
	}
	return &payload, nil
}

func (facts FunctionScalarFacts) Restore(index *FunctionSourceIndex) (FunctionScalarObservations, error) {
	if err := validateScalarObservationSchema(reflect.TypeOf(FunctionScalarObservations{})); err != nil {
		return FunctionScalarObservations{}, err
	}
	payload, err := decodeScalarFacts(facts.encoded)
	if err != nil {
		return FunctionScalarObservations{}, err
	}
	if index == nil || payload.FunctionKey != index.key || payload.Tree != index.digest {
		return FunctionScalarObservations{}, fmt.Errorf("scalar product does not match function inputs")
	}
	anchors := make([]FunctionSourceAnchor, len(payload.Entries))
	for i, entry := range payload.Entries {
		anchors[i] = FunctionSourceAnchor{FunctionKey: payload.FunctionKey, Tree: payload.Tree, Ordinal: entry.Anchor.Ordinal, Kind: entry.Anchor.Kind}
	}
	nodes, err := index.Rebind(anchors)
	if err != nil {
		return FunctionScalarObservations{}, err
	}
	output := FunctionScalarObservations{
		ExprTypes: map[ast.Expr]Type{}, ResolvedTypeNames: map[ast.TypeExpr]string{}, ResolvedValueNames: map[*ast.Ident]string{},
		PermGrowthOps: map[ast.Expr]bool{}, RewriteDefaults: map[*ast.Ident]bool{}, IndexBoundsProven: map[*ast.IndexExpr]bool{},
		LemmaCalls: map[*ast.CallExpr]bool{}, GhostDecls: map[*ast.VarDeclStmt]bool{}, GhostContracts: map[ast.Expr]bool{},
	}
	for i, entry := range payload.Entries {
		node := nodes[i]
		switch entry.Field {
		case "ExprTypes":
			n, ok := node.(ast.Expr)
			if !ok {
				return FunctionScalarObservations{}, fmt.Errorf("expression fact has non-expression anchor")
			}
			typ, err := restoreScalarType(entry.Type)
			if err != nil {
				return FunctionScalarObservations{}, err
			}
			output.ExprTypes[n] = typ
		case "ResolvedTypeNames":
			n, ok := node.(ast.TypeExpr)
			if !ok {
				return FunctionScalarObservations{}, fmt.Errorf("type name fact has non-type anchor")
			}
			output.ResolvedTypeNames[n] = entry.Text
		case "ResolvedValueNames":
			n, ok := node.(*ast.Ident)
			if !ok {
				return FunctionScalarObservations{}, fmt.Errorf("value name fact has non-identifier anchor")
			}
			output.ResolvedValueNames[n] = entry.Text
		case "PermGrowthOps", "GhostContracts":
			n, ok := node.(ast.Expr)
			if !ok {
				return FunctionScalarObservations{}, fmt.Errorf("boolean fact has non-expression anchor")
			}
			if entry.Field == "PermGrowthOps" {
				output.PermGrowthOps[n] = entry.Flag
			} else {
				output.GhostContracts[n] = entry.Flag
			}
		case "RewriteDefaults":
			n, ok := node.(*ast.Ident)
			if !ok {
				return FunctionScalarObservations{}, fmt.Errorf("rewrite fact has non-identifier anchor")
			}
			output.RewriteDefaults[n] = entry.Flag
		case "IndexBoundsProven":
			n, ok := node.(*ast.IndexExpr)
			if !ok {
				return FunctionScalarObservations{}, fmt.Errorf("bounds fact has non-index anchor")
			}
			output.IndexBoundsProven[n] = entry.Flag
		case "LemmaCalls":
			n, ok := node.(*ast.CallExpr)
			if !ok {
				return FunctionScalarObservations{}, fmt.Errorf("lemma fact has non-call anchor")
			}
			output.LemmaCalls[n] = entry.Flag
		case "GhostDecls":
			n, ok := node.(*ast.VarDeclStmt)
			if !ok {
				return FunctionScalarObservations{}, fmt.Errorf("ghost fact has non-variable anchor")
			}
			output.GhostDecls[n] = entry.Flag
		}
	}
	return output, nil
}
