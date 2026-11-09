package semantic

import (
	"reflect"
	"testing"
)

// Every embedded group must participate in per-field cloning and merging.
func TestAnalyzerStorageFieldsCoverEmbeddedState(t *testing.T) {
	typ := reflect.TypeFor[Analyzer]()
	got := make(map[string][]int, len(analyzerStorageFields))
	for _, field := range analyzerStorageFields {
		if _, duplicate := got[field.Name]; duplicate {
			t.Fatalf("duplicate storage field %s", field.Name)
		}
		got[field.Name] = field.Index
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.Anonymous {
			continue
		}
		for j := 0; j < field.Type.NumField(); j++ {
			nested := field.Type.Field(j)
			if !reflect.DeepEqual(got[nested.Name], []int{i, j}) {
				t.Errorf("embedded field %s missing or misindexed", nested.Name)
			}
		}
	}
}
