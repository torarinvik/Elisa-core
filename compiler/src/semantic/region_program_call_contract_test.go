package semantic

import (
	"strings"
	"testing"
)

func TestProgramRegionViewCallContract(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		reject      bool
	}{
		{"literal", `"safe"`, false},
		{"program_parameter", "stable", false},
		{"local_view", "bytes.as_sview()", true},
		{"borrowed_parameter", "borrowed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `global region source
def remember(names: mutable darray[sview @source]&, text: sview @source) -> void:
    can Memory.Allocate, Abort.Panic:
        names.push(text)
def caller(stable: sview @source, borrowed: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview @source] = []
        region short(4096):
            bytes: mutable darray[u8] @short = [65]
            remember(names, ` + tc.value + `)
`
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, tc.name+".elisa", source)
			errors := result.Errors()
			if tc.reject {
				found := false
				for _, err := range errors {
					if strings.Contains(err, "region") {
						found = true
					}
				}
				if !found {
					t.Fatalf("expected lifetime rejection, got %v", errors)
				}
			} else if len(errors) != 0 {
				t.Fatalf("valid program-lived argument rejected: %v", errors)
			}
		})
	}
}
