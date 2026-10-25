package semantic

import (
	"strings"
	"testing"
)

const programRecordChoiceSource = `global region source

struct Subset:
    restricted: bool
    variants: darray[sview @source] @source

def make_subset(name: sview @source) -> Subset:
    can Memory.Allocate, Abort.Panic:
        variants: mutable darray[sview @source] @source = []
        variants.push(name)
        return Subset{restricted: true, variants: variants}

def choose(name: sview @source, pick: bool) -> Subset:
    can Memory.Allocate, Abort.Panic:
        empty_variants: darray[sview @source] @source = []
        empty: Subset = Subset{restricted: false, variants: empty_variants}
        subset: Subset = make_subset(name) if pick else empty
        return subset
`

func TestProgramRecordProducerChoiceKeepsDeclaredStorage(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "program_record_choice.elisa", programRecordChoiceSource, AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("source-owned record choice rejected: %v", errors)
	}
}

func TestProgramRecordChoiceStillRejectsLocalStorage(t *testing.T) {
	cases := []struct{ name, source string }{
		{"source_field", `global region source
struct Item:
    text: sview @source
def bad() -> Item:
    can Memory.Allocate, Abort.Panic:
        text: mutable dstr = []
        text.push(65)
        return Item{text: text.as_sview()}
`},
		{"mixed_fields_and_choice", `global region source
struct Mixed:
    safe: sview @source
    values: darray[sview]
def bad(pick: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[Mixed] @source = []
        region short(4096):
            text: mutable dstr = []
            text.push(65)
            values: mutable darray[sview] = []
            values.push(text.as_sview())
            local: Mixed = Mixed{safe: "safe", values: values}
            empty: Mixed = Mixed{safe: "safe", values: []}
            out.push(local if pick else empty)
`},
		{"hidden_enum_storage", `global region source
enum Payload:
    Empty
    Items(values: darray[sview])
struct Mixed:
    safe: sview @source
    payload: Payload
def bad(pick: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[Mixed] @source = []
        region short(4096):
            text: mutable dstr = []
            text.push(65)
            values: mutable darray[sview] = []
            values.push(text.as_sview())
            local: Mixed = Mixed{safe: "safe", payload: Payload.Items(values)}
            empty: Mixed = Mixed{safe: "safe", payload: Payload.Empty}
            out.push(local if pick else empty)
`},
		{"program_and_local_branches", `global region source
def bad(pick: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview @source] @source = []
        region short(4096):
            text: mutable dstr = []
            text.push(65)
            out.push(text.as_sview() if pick else "safe")
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, c.name+".elisa", c.source, AnalyzeOptions{})
			found := false
			for _, err := range result.Errors() {
				if strings.Contains(err, "region") && (strings.Contains(err, "dangling") || strings.Contains(err, "outlive") || strings.Contains(err, "program-lived")) {
					found = true
				}
			}
			if !found {
				t.Fatalf("local storage escape lost its lifetime diagnostic: %v", result.Errors())
			}
		})
	}
}

func TestProgramRecordContainerElementsDoNotAcquireLocalDependencies(t *testing.T) {
	const src = `global region source
struct Item:
    name: sview @source

def collect(name: sview @source) -> sview @source:
    can Memory.Allocate, Abort.Panic:
        items: mutable darray[Item] = []
        items.push(Item{name: name})
        return items[0].name

def helper(name: sview @source) -> Item:
    return Item{name: name}

def collect_helper(name: sview @source) -> sview @source:
    can Memory.Allocate, Abort.Panic:
        items: mutable darray[Item] = []
        items.push(helper(name))
        return items[0].name
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "program_record_elements.elisa", src, AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("program-owned field recovered from local container rejected: %v", errors)
	}
}

func TestProgramRecordRegionShadowIsRejected(t *testing.T) {
	const src = `global region source
struct Item:
    name: sview @source
def bad() -> void:
    can Memory.Allocate, Abort.Panic:
        region source(4096):
            item: Item = Item{name: "text"}
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "program_record_shadow.elisa", src, AnalyzeOptions{})
	found := false
	for _, err := range result.Errors() {
		if strings.Contains(err, "shadows the program-lived region") {
			found = true
		}
	}
	if !found {
		t.Fatalf("region shadow not rejected: %v", result.Errors())
	}
}
