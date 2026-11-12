package semantic

import "reflect"

// analyzerStorageFieldName reports the top-level logical field for a walk path.
// The anonymous state groups are an organizational detail of that schema.
func analyzerStorageFieldName(path []int) string {
	t := reflect.TypeFor[Analyzer]()
	field := t.Field(path[0])
	if field.Anonymous && len(path) > 1 {
		return field.Type.Field(path[1]).Name
	}
	return field.Name
}
