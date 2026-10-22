package semantic

import (
	"reflect"
	"testing"
)

func TestParallelCloneArraysDeepCloneNestedMaps(t *testing.T) {
	type nested struct {
		entries [2]map[string]int
	}
	source := map[string]nested{
		"row": {entries: [2]map[string]int{
			{"value": 1},
			{"value": 2},
		}},
		"other": {entries: [2]map[string]int{
			{"value": 4},
			{"value": 5},
		}},
	}
	clone := deepCloneValue(reflect.ValueOf(source)).Interface().(map[string]nested)

	entry := clone["row"]
	entry.entries[0]["value"] = 3
	clone["row"] = entry
	if source["row"].entries[0]["value"] != 1 {
		t.Fatal("nested map in struct array aliases the source")
	}
	if clone["row"].entries[1]["value"] != 2 {
		t.Fatal("cloning one array element changed another")
	}
	other := clone["other"]
	other.entries[0]["value"] = 6
	clone["other"] = other
	if source["other"].entries[0]["value"] != 4 || clone["row"].entries[0]["value"] != 3 {
		t.Fatal("reused map struct slot aliased nested maps across entries")
	}
	if clone["other"].entries[1]["value"] != 5 {
		t.Fatal("cloning one entry's array changed its sibling element")
	}

	directSource := map[int][1]map[string]int{
		0: {map[string]int{"value": 4}},
		1: {map[string]int{"value": 6}},
	}
	directClone := deepCloneValue(reflect.ValueOf(directSource)).Interface().(map[int][1]map[string]int)
	value := directClone[0]
	value[0]["value"] = 5
	directClone[0] = value
	if directSource[0][0]["value"] != 4 {
		t.Fatal("nested map in array map value aliases the source")
	}
	otherValue := directClone[1]
	otherValue[0]["value"] = 7
	directClone[1] = otherValue
	if directSource[1][0]["value"] != 6 || directClone[0][0]["value"] != 5 {
		t.Fatal("array map values alias across entries")
	}
}
