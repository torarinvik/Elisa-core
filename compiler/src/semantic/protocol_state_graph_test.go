package semantic

import (
	"strings"
	"testing"
)

const protocolGraphPrelude = `affine struct File[state Closed | Open]:
    handle: i64
    transitions:
        Closed -> Open
        Open -> Closed

`

func TestProtocolGraphIndependentFunctions(t *testing.T) {
	analyzeTreeTestSource(t, "protocol_graph.elisa", protocolGraphPrelude+`def first(file: File[Closed]) -> File[Open]:
    transition[Open](move file)

def second(file: File[Closed]) -> File[Open]:
    transition[Open](move file)

def initial() -> File[Closed]:
    File[Closed]{handle: 7}
`)
}

func TestProtocolGraphRejectsForgery(t *testing.T) {
	cases := []struct{ name, source, message string }{
		{"constructor", `def forge() -> File[Open]:
    File[Open]{handle: 7}
`, "only be constructed in initial state"},
		{"zeroed", `def forge() -> File[Open]:
    zeroed
`, "zeroed cannot manufacture"},
		{"nested_zeroed", `struct Wrapper:
    file: File[Open]

def forge() -> Wrapper:
    zeroed
`, "zeroed cannot manufacture"},
		{"missing_move", `def forge(file: File[Closed]) -> File[Open]:
    transition[Open](file)
`, "requires explicit move"},
		{"unknown_state", `def forge(file: File[Closed]) -> File[Open]:
    transition[Missing](move file)
`, "unknown protocol state"},
		{"old_owner", `def forge(file: File[Closed]) -> i64:
    opened: File[Open] = transition[Open](move file)
    file.handle
`, "consumed"},
		{"borrow_after_move", `def forge(file: File[Closed]) -> i64:
    borrowed: File[Closed]& = &file
    opened: File[Open] = transition[Open](move file)
    borrowed.handle
`, "consumed"},
		{"cast", `def forge(file: File[Closed]) -> File[Open]:
    file.cast[File[Open]]
`, "cast"},
		{"borrowed_transition", `def forge(file: File[Closed]&) -> File[Open]:
    transition[Open](move file)
`, "owned state-qualified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", protocolGraphPrelude+tc.source, AnalyzeOptions{})
			if diagnostics := allDiagnostics(result); !strings.Contains(diagnostics, tc.message) {
				t.Fatalf("wanted %q, got:\n%s", tc.message, diagnostics)
			}
		})
	}
}

func TestProtocolGraphMissingEdge(t *testing.T) {
	source := strings.Replace(protocolGraphPrelude, "        Closed -> Open\n", "", 1) + `def invalid(file: File[Closed]) -> File[Open]:
    transition[Open](move file)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "missing_edge.elisa", source, AnalyzeOptions{})
	if diagnostics := allDiagnostics(result); !strings.Contains(diagnostics, "illegal protocol transition Closed -> Open") {
		t.Fatal(diagnostics)
	}
}

func TestProtocolGraphValidation(t *testing.T) {
	for _, tc := range []struct{ name, source, message string }{
		{"duplicate", strings.Replace(protocolGraphPrelude, "        Closed -> Open", "        Closed -> Open\n        Closed -> Open", 1), "duplicate protocol transition"},
		{"unknown", strings.Replace(protocolGraphPrelude, "Open -> Closed", "Open -> Missing", 1), "undeclared state"},
		{"copied_owner", strings.Replace(protocolGraphPrelude, "affine struct", "struct", 1) + `def invalid(file: File[Closed]) -> File[Open]:
    transition[Open](move file)
`, "linear or affine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", tc.source, AnalyzeOptions{})
			if diagnostics := allDiagnostics(result); !strings.Contains(diagnostics, tc.message) {
				t.Fatal(diagnostics)
			}
		})
	}
}

func TestProtocolGraphModuleAuthority(t *testing.T) {
	owned := "module Vault:\n" + "    " + strings.ReplaceAll(strings.TrimSpace(protocolGraphPrelude), "\n", "\n    ") + "\n\n"
	for _, tc := range []struct{ name, source string }{
		{"construction", `def forge() -> Vault::File[Closed]:
    Vault::File[Closed]{handle: 7}
`},
		{"transition", `def forge(file: Vault::File[Closed]) -> Vault::File[Open]:
    transition[Open](move file)
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", owned+tc.source, AnalyzeOptions{})
			if diagnostics := allDiagnostics(result); !strings.Contains(diagnostics, "private to its owning module") {
				t.Fatal(diagnostics)
			}
		})
	}
}
