package semantic

import "testing"

// Aggregate wrapping must preserve one known parameter region, while conflicting
// or unknown child provenance must never acquire a lifetime proof.
func TestCurrentParamRegionFromFieldOnlyProvenance(t *testing.T) {
	a := &Analyzer{currentFuncType: &FuncType{Params: []Type{
		&SViewType{Region: "r"}, &SViewType{Region: "s"},
	}}}
	r := regionRefState{HasDirectParamDep: true, DirectParamDep: 0}
	s := regionRefState{HasDirectParamDep: true, DirectParamDep: 1}
	for _, tc := range []struct {
		name  string
		state regionRefState
		want  string
		known bool
	}{
		{"direct", r, "r", true},
		{"one-field", regionRefState{Fields: map[string]regionRefState{"payload": r}}, "r", true},
		{"two-same-region-fields", regionRefState{Fields: map[string]regionRefState{"left": r, "right": r}}, "r", true},
		{"nested-field", regionRefState{Fields: map[string]regionRefState{"outer": {Fields: map[string]regionRefState{"inner": r}}}}, "r", true},
		{"conflicting-fields", regionRefState{Fields: map[string]regionRefState{"left": r, "right": s}}, "", false},
		{"unknown-field", regionRefState{Fields: map[string]regionRefState{"known": r, "unknown": {}}}, "", false},
		{"root-and-field-same", regionRefState{HasDirectParamDep: true, DirectParamDep: 0, Fields: map[string]regionRefState{"payload": r}}, "r", true},
		{"root-and-field-conflict", regionRefState{HasDirectParamDep: true, DirectParamDep: 0, Fields: map[string]regionRefState{"payload": s}}, "", false},
		{"local-field", regionRefState{Fields: map[string]regionRefState{"payload": {Deps: map[*Symbol]regionDependencyState{&Symbol{Name: "local"}: {Valid: true}}}}}, "", false},
		{"packed-field", regionRefState{Fields: map[string]regionRefState{"payload": {StoreDeps: map[*Symbol]packedStoreDependencyState{&Symbol{Name: "store"}: {}}}}}, "", false},
		{"bad-param-field", regionRefState{Fields: map[string]regionRefState{"payload": {HasDirectParamDep: true, DirectParamDep: 99}}}, "", false},
		{"root-local-with-field", regionRefState{Deps: map[*Symbol]regionDependencyState{&Symbol{Name: "local"}: {Valid: true}}, Fields: map[string]regionRefState{"payload": r}}, "", false},
		{"root-store-with-field", regionRefState{StoreDeps: map[*Symbol]packedStoreDependencyState{&Symbol{Name: "store"}: {}}, Fields: map[string]regionRefState{"payload": r}}, "", false},
		{"empty-fields", regionRefState{Fields: map[string]regionRefState{}}, "", false},
		{"empty", regionRefState{}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, known := a.currentParamRegionFromRefState(tc.state)
			if known != tc.known || (known && got != tc.want) {
				t.Fatalf("region=%q known=%v, want region=%q known=%v", got, known, tc.want, tc.known)
			}
		})
	}
}
