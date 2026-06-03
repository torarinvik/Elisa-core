package semantic

import "testing"

// fieldsHaveRegionProvenance drives promote's shallow-rule rejection: a value with
// any interior (field/element) region provenance cannot be shallow-promoted.
func TestFieldsHaveRegionProvenance(t *testing.T) {
	region := &Symbol{}

	// A flat value (only its own top-level backing) has no interior provenance.
	flat := regionRefState{
		Deps: map[*Symbol]regionDependencyState{region: {Valid: true}},
	}
	if fieldsHaveRegionProvenance(flat) {
		t.Error("a flat region-backed value must report no interior provenance")
	}

	// A value with a region-backed field reports interior provenance.
	withField := regionRefState{
		Fields: map[string]regionRefState{
			"node": {Deps: map[*Symbol]regionDependencyState{region: {Valid: true}}},
		},
	}
	if !fieldsHaveRegionProvenance(withField) {
		t.Error("expected interior region provenance to be detected")
	}

	// Detection recurses through nested fields.
	nested := regionRefState{
		Fields: map[string]regionRefState{
			"outer": {Fields: map[string]regionRefState{
				"inner": {Deps: map[*Symbol]regionDependencyState{region: {Valid: true}}},
			}},
		},
	}
	if !fieldsHaveRegionProvenance(nested) {
		t.Error("expected nested interior provenance to be detected recursively")
	}
}
