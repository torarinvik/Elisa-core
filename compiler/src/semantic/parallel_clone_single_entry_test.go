package semantic

import (
	"reflect"
	"testing"
)

func TestParallelCloneSingleEntryMapWithUnexportedNestedMaps(t *testing.T) {
	type inner struct {
		values map[string]int
	}
	type nested struct {
		one  inner
		many [1]inner
	}
	source := map[string]nested{
		"only": {
			one:  inner{values: map[string]int{"value": 1}},
			many: [1]inner{{values: map[string]int{"value": 2}}},
		},
	}
	clone := deepCloneValue(reflect.ValueOf(source)).Interface().(map[string]nested)

	entry := clone["only"]
	entry.one.values["value"] = 3
	entry.many[0].values["value"] = 4
	clone["only"] = entry

	if source["only"].one.values["value"] != 1 {
		t.Fatal("nested map in one-entry struct value aliases the source")
	}
	if source["only"].many[0].values["value"] != 2 {
		t.Fatal("nested map in one-entry array value aliases the source")
	}
}
