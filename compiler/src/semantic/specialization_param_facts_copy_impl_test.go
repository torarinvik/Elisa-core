package semantic

import "reflect"

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
