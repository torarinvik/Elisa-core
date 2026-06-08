package semantic

import (
	"fmt"
	"reflect"
	"strings"

	"elisacore/src/ast"
)

func formatDiagnosticArgs(args []interface{}) []interface{} {
	if len(args) == 0 {
		return nil
	}
	formatted := make([]interface{}, len(args))
	for i, arg := range args {
		formatted[i] = formatDiagnosticArg(arg)
	}
	return formatted
}

func formatDiagnosticArg(arg interface{}) interface{} {
	switch value := arg.(type) {
	case Type:
		return diagnosticTypeString(value)
	default:
		return arg
	}
}

func diagnosticTypeString(t Type) string {
	if t == nil {
		return "<invalid>"
	}
	value := reflect.ValueOf(t)
	if value.Kind() == reflect.Ptr && value.IsNil() {
		return "<invalid>"
	}
	if replacement, ok := diagnosticRuntimeCarrierTypeString(t); ok {
		return replacement
	}
	switch tt := t.(type) {
	case *ErrorUnionType:
		if tt == nil || tt.Value == nil || tt.Errors == nil {
			return "<invalid-error-union>"
		}
		return fmt.Sprintf("%s | %s", diagnosticTypeString(tt.Value), ErrorSetDiagnosticName(tt.Errors))
	case *OptionalType:
		if tt == nil || tt.Value == nil {
			return "<invalid-optional>"
		}
		return diagnosticTypeString(tt.Value) + "?"
	case *TupleType:
		if tt == nil {
			return "<invalid-tuple>"
		}
		parts := make([]string, 0, len(tt.Fields))
		for _, field := range tt.Fields {
			if field.Type == nil {
				if field.Name != "" {
					parts = append(parts, field.Name+": <invalid>")
				} else {
					parts = append(parts, "<invalid>")
				}
				continue
			}
			if field.Name != "" {
				parts = append(parts, field.Name+": "+diagnosticTypeString(field.Type))
				continue
			}
			parts = append(parts, diagnosticTypeString(field.Type))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *RefType:
		if tt == nil || tt.Elem == nil {
			return "<invalid-ref>"
		}
		s := diagnosticTypeString(tt.Elem)
		if tt.Mutable {
			s = "mutable " + s
		}
		// Storage class stays a prefix; region provenance is the canonical `@r` suffix.
		if tt.Storage != RefStorageAny {
			s = RefStorageName(tt.Storage) + " " + s
		}
		region := ""
		if tt.Region != "" {
			region = " @" + tt.Region
		}
		switch tt.State {
		case RefStateNullable:
			return s + "&?" + region
		case RefStateNull:
			return s + "!" + region
		default:
			return s + "&" + region
		}
	case *ArrayType:
		if tt == nil || tt.Elem == nil {
			return "<invalid-array>"
		}
		if tt.SurfaceName == "str" || tt.SurfaceName == "string" {
			return fmt.Sprintf("str[%s]", tt.Size)
		}
		if tt.SurfaceName == "array" {
			return fmt.Sprintf("array[%s, %s]", diagnosticTypeString(tt.Elem), tt.Size)
		}
		return fmt.Sprintf("%s[%s]", diagnosticTypeString(tt.Elem), tt.Size)
	case *DArrayType:
		if tt == nil || tt.Elem == nil {
			return "<invalid-darray>"
		}
		if isWildcardShape(tt.Shape) {
			return fmt.Sprintf("darray[%s]", diagnosticTypeString(tt.Elem))
		}
		return fmt.Sprintf("darray[%s, %s]", diagnosticTypeString(tt.Elem), tt.Shape.String())
	case *ViewType:
		if tt == nil || tt.Elem == nil {
			return "<invalid-view>"
		}
		if tt.Begin != "" || tt.End != "" {
			return fmt.Sprintf("view[%s, %s, %s]", diagnosticTypeString(tt.Elem), tt.Begin, tt.End)
		}
		return fmt.Sprintf("view[%s]", diagnosticTypeString(tt.Elem))
	case *DArrayViewType:
		if tt == nil || tt.Elem == nil {
			return "<invalid-dview>"
		}
		return fmt.Sprintf("dview[%s]", diagnosticTypeString(tt.Elem))
	case *StoreRowsViewType:
		if tt == nil || tt.Store == nil {
			return "<invalid-store-rows>"
		}
		return diagnosticTypeString(tt.Store) + ".rows()"
	case *StoreRowViewType:
		if tt == nil || tt.Store == nil {
			return "<invalid-store-row>"
		}
		return diagnosticTypeString(tt.Store) + ".row"
	case *DStrType:
		if tt == nil {
			return "<invalid-cstr>"
		}
		if isWildcardShape(tt.Shape) {
			return "cstr"
		}
		return fmt.Sprintf("cstr[%s]", tt.Shape.String())
	case *DictType:
		if tt == nil || tt.Key == nil || tt.Value == nil {
			return "<invalid-dict>"
		}
		return fmt.Sprintf("dict[%s, %s]", diagnosticTypeString(tt.Key), diagnosticTypeString(tt.Value))
	case *SetType:
		if tt == nil || tt.Elem == nil {
			return "<invalid-set>"
		}
		return fmt.Sprintf("set[%s]", diagnosticTypeString(tt.Elem))
	case *DictEntryType:
		if tt == nil || tt.Dict == nil {
			return "<invalid-dict-entry>"
		}
		if tt.Mutable {
			return fmt.Sprintf("dict.entry[mutable %s, %s]", diagnosticTypeString(tt.Dict.Key), diagnosticTypeString(tt.Dict.Value))
		}
		return fmt.Sprintf("dict.entry[%s, %s]", diagnosticTypeString(tt.Dict.Key), diagnosticTypeString(tt.Dict.Value))
	case *SViewType:
		if tt == nil {
			return "<invalid-sview>"
		}
		if tt.Begin == "" && tt.End == "" {
			return "sview"
		}
		return fmt.Sprintf("sview[%s, %s]", tt.Begin, tt.End)
	case *AssociatedTypeProjection:
		if tt == nil || tt.Receiver == nil {
			return "<invalid-associated-type>"
		}
		return diagnosticTypeString(tt.Receiver) + "." + tt.Name
	case *AggregateStateType:
		if tt == nil || tt.Base == nil {
			return "<invalid-aggregate-state>"
		}
		states := aggregateStateStates(tt)
		parts := make([]string, 0, len(states))
		for _, state := range states {
			parts = append(parts, ast.RefStateMarker(ast.RefState(state)))
		}
		return fmt.Sprintf("%s[%s]", diagnosticTypeString(tt.Base), strings.Join(parts, ", "))
	case *GenericInstanceType:
		if tt == nil {
			return "<invalid-generic-instance>"
		}
		parts := make([]string, 0, len(tt.Args))
		for _, arg := range tt.Args {
			parts = append(parts, diagnosticTypeString(arg))
		}
		return fmt.Sprintf("%s[%s]", tt.Name, strings.Join(parts, ", "))
	case *FuncType:
		if tt == nil {
			return "<invalid-func>"
		}
		explicitCount := funcTypeExplicitParamCount(tt)
		if explicitCount > len(tt.Params) {
			explicitCount = len(tt.Params)
		}
		parts := make([]string, 0, explicitCount)
		for _, p := range tt.Params[:explicitCount] {
			parts = append(parts, diagnosticTypeString(p))
		}
		implicitParts := make([]string, 0, len(tt.ImplicitParamNames))
		for i, name := range tt.ImplicitParamNames {
			index := explicitCount + i
			if index >= len(tt.Params) || tt.Params[index] == nil {
				implicitParts = append(implicitParts, name+": <invalid>")
				continue
			}
			implicitParts = append(implicitParts, name+": "+diagnosticTypeString(tt.Params[index]))
		}
		generics := make([]string, 0, len(tt.GenericParams)+len(tt.RegionParams)+len(tt.PermissionParams))
		if len(tt.GenericParams) != 0 {
			for _, param := range tt.GenericParams {
				if param.InterfaceBound != "" {
					generics = append(generics, param.Name+": "+param.InterfaceBound)
				} else {
					generics = append(generics, param.Name)
				}
			}
		} else {
			for _, param := range tt.TypeParams {
				generics = append(generics, param)
			}
		}
		for _, param := range tt.RegionParams {
			generics = append(generics, "region "+param)
		}
		for _, param := range tt.PermissionParams {
			generics = append(generics, "permission "+param)
		}
		prefix := ""
		if len(generics) > 0 {
			prefix = "[" + strings.Join(generics, ", ") + "]"
		}
		if tt.Variadic {
			parts = append(parts, "...")
		}
		withClause := ""
		if len(implicitParts) != 0 {
			withClause = " with " + strings.Join(implicitParts, ", ")
		}
		if tt.Return == nil {
			return fmt.Sprintf("func%s(%s)%s%s", prefix, strings.Join(parts, ", "), withClause, permissionFamiliesString(tt.Permissions))
		}
		return fmt.Sprintf("func%s(%s)%s -> %s%s", prefix, strings.Join(parts, ", "), withClause, diagnosticTypeString(tt.Return), permissionFamiliesString(tt.Permissions))
	default:
		return safeDiagnosticTypeFallback(t)
	}
}

func safeDiagnosticTypeFallback(t Type) (result string) {
	defer func() {
		if recover() != nil {
			result = "<invalid>"
		}
	}()
	return t.String()
}

func diagnosticRuntimeCarrierTypeString(t Type) (string, bool) {
	if t == nil {
		return "", false
	}
	switch tt := t.(type) {
	case *StructType:
		if tt.Name == "DynDict" {
			return "dict[K, V] (runtime carrier)", true
		}
		if tt.Name == "DynSet" {
			return "set[T] (runtime carrier)", true
		}
		return runtimeCarrierTypeDisplayReplacement(tt.Name)
	case *GenericInstanceType:
		switch tt.Name {
		case "DynArray":
			if len(tt.Args) == 1 {
				return fmt.Sprintf("darray[%s, shape]", diagnosticTypeString(tt.Args[0])), true
			}
			return runtimeCarrierTypeDisplayReplacement(tt.Name)
		case "DynDict":
			if len(tt.Args) == 2 {
				dictText := fmt.Sprintf("dict[%s, %s]", diagnosticTypeString(tt.Args[0]), diagnosticTypeString(tt.Args[1]))
				dictType := &DictType{Key: tt.Args[0], Value: tt.Args[1]}
				if dictSupportsRuntimeBackedOps(dictType) {
					return dictText, true
				}
				return dictText + " (runtime carrier)", true
			}
			return "dict[K, V] (runtime carrier)", true
		case "DynSet":
			if len(tt.Args) == 1 {
				setText := fmt.Sprintf("set[%s]", diagnosticTypeString(tt.Args[0]))
				setType := &SetType{Elem: tt.Args[0]}
				if setSupportsRuntimeBackedOps(setType) {
					return setText, true
				}
				return setText + " (runtime carrier)", true
			}
			return "set[T] (runtime carrier)", true
		default:
			return "", false
		}
	default:
		return "", false
	}
}
