package semantic_test

import (
	"strings"
	"testing"
)

// A parameter's type is resolved when the signature is collected AND again when the body
// is analyzed, so an unresolvable one used to be reported twice at the same span. The same
// position, severity and text carry no new information the second time.
func TestAnalyzeReportsOneDiagnosticPerIdenticalSpan(t *testing.T) {
	src := `def take(p: Thing) -> int:
	return 0

def main() -> int:
	return take(0)
`
	_, errs := parseAndAnalyze(t, "duplicate_diagnostic.elisa", src)
	seen := 0
	for _, err := range errs {
		if strings.Contains(err, `unknown type "Thing"`) {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("expected exactly one `unknown type \"Thing\"`, got %d: %v", seen, errs)
	}
}

// An unguarded irrefutable struct pattern matches every value of its struct scrutinee, so
// a match made only of one is a terminator: the function does not fall through. Before
// this, only a literal `_` arm counted and the backend refused the program.
func TestAnalyzeIrrefutableStructArmTerminatesMatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"brace", "struct Item:\n\tv: int\n\ndef pick(it: Item) -> int:\n\tmatch it:\n\t\tItem{v}:\n\t\t\treturn v\n"},
		{"paren", "struct Item:\n\tv: int\n\ndef pick(it: Item) -> int:\n\tmatch it:\n\t\tItem(v: v):\n\t\t\treturn v\n"},
		{"qualified", "module Pack:\n\tstruct Item:\n\t\tv: int\n\ndef pick(it: Pack::Item) -> int:\n\tmatch it:\n\t\tPack::Item{v}:\n\t\t\treturn v\n"},
	} {
		_, errs := parseAndAnalyze(t, "irrefutable_struct_arm.elisa", tc.src)
		for _, err := range errs {
			if strings.Contains(err, "fall through") {
				t.Fatalf("%s: match of an irrefutable struct arm reported as falling through: %v", tc.name, errs)
			}
		}
		if len(errs) != 0 {
			t.Fatalf("%s: unexpected diagnostics: %v", tc.name, errs)
		}
	}
}
