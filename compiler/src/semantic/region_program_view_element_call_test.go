package semantic

import (
	"strings"
	"testing"
)

func TestProgramRegionViewElementCallContract(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, value string
		reject                   bool
	}{
		{"source_elements_local_buffer", "names: mutable darray[sview @source] = [\"safe\"]", "names", false},
		{"source_array_parameter", "", "stable", false},
		{"other_program_region", "", "permanent", false},
		{"short_elements_local_buffer", "names: mutable darray[sview] = [bytes.as_sview()]", "names", true},
		{"borrowed_array_parameter", "", "borrowed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `global region source
global region permanent
def first_name(names: darray[sview @source]&) -> sview @source:
    can Abort.Panic:
        return names[0]
def caller(stable: darray[sview @source]&, permanent: darray[sview @permanent]&, borrowed: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        region short(4096):
            bytes: mutable darray[u8] @short = [65]
`
			if tc.declaration != "" {
				source += "            " + tc.declaration + "\n"
			}
			source += "            text: sview @source = first_name(" + tc.value + ")\n"
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, tc.name+".elisa", source)
			errors := result.Errors()
			if tc.reject {
				found := false
				for _, err := range errors {
					if strings.Contains(err, "argument") && strings.Contains(err, "region") {
						found = true
					}
				}
				if !found {
					t.Fatalf("expected element lifetime argument rejection, got %v", errors)
				}
			} else if len(errors) != 0 {
				t.Fatalf("valid element lifetime rejected: %v", errors)
			}
		})
	}
}
