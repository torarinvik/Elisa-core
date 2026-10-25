package semantic

import "reflect"

// Exact old reflective struct policy, including opaque interfaces and shared
// slice backing. Unknown future fields/types keep the ordinary clone path.
var specializationParamFactsCopySchemaValid = specializationParamFactsSchemaMatches(reflect.TypeFor[storeScanParamFacts]())

func specializationParamFactsSchemaMatches(t reflect.Type) bool {
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	names := []string{"why", "at", "writes", "reach"}
	types := []reflect.Type{reflect.TypeFor[string](), reflect.TypeFor[any](), reflect.TypeFor[bool](), reflect.TypeFor[[]storeScanParamKey]()}
	if t.NumField() != len(names) {
		return false
	}
	for i, name := range names {
		if t.Field(i).Name != name || t.Field(i).Type != types[i] {
			return false
		}
	}
	return true
}

func cloneSpecializationParamFacts(source map[storeScanParamKey]storeScanParamFacts) map[storeScanParamKey]storeScanParamFacts {
	if !specializationParamFactsCopySchemaValid {
		return deepCloneValue(reflect.ValueOf(source)).Interface().(map[storeScanParamKey]storeScanParamFacts)
	}
	if source == nil {
		return nil
	}
	out := make(map[storeScanParamKey]storeScanParamFacts, len(source))
	for key, value := range source {
		if value.reach != nil {
			value.reach = value.reach[:len(value.reach):len(value.reach)]
		}
		out[key] = value
	}
	return out
}
