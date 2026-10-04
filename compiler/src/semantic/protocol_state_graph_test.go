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

func TestProtocolGraphEveryIncomingState(t *testing.T) {
	prelude := strings.Replace(protocolGraphPrelude, "        Open -> Closed\n", "", 1)
	for _, tc := range []struct{ name, source, message string }{
		{"union_rejects_illegal_peer", `def invalid(file: File[Closed | Open]) -> File[Closed]:
    transition[Closed](move file)
`, "illegal protocol transition Open -> Closed"},
		{"reordered_union_rejects_illegal_peer", `def invalid(file: File[Open | Closed]) -> File[Closed]:
    transition[Closed](move file)
`, "illegal protocol transition Open -> Closed"},
		{"borrowed_alias", `type Borrowed = File[Closed]&
type Again = Borrowed
def invalid(file: Again) -> File[Open]:
    transition[Open](move file)
`, "owned state-qualified"},
		{"linear_mutable_alias", `type Borrowed = lmut File[Closed]
def invalid(file: Borrowed) -> File[Open]:
    transition[Open](move file)
`, "owned state-qualified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", prelude+tc.source, AnalyzeOptions{})
			if diagnostics := allDiagnostics(result); !strings.Contains(diagnostics, tc.message) {
				t.Fatalf("wanted %q, got:\n%s", tc.message, diagnostics)
			}
		})
	}
	for _, source := range []string{
		`def valid(file: File[Closed | Open]) -> File[Open]:
    transition[Open](move file)
`,
		`type Owned = File[Closed]
def valid(file: Owned) -> File[Open]:
    transition[Open](move file)
`,
	} {
		analyzeTreeTestSource(t, "all_paths_valid.elisa", prelude+source)
	}
}

func TestProtocolGraphAuthorityBoundaries(t *testing.T) {
	owned := "module Vault:\n    " + strings.ReplaceAll(strings.TrimSpace(protocolGraphPrelude), "\n", "\n    ") + "\n\n"
	for _, tc := range []struct{ name, source string }{
		{"peer", "module Peer:\n    def invalid(file: Vault::File[Closed]) -> Vault::File[Open]:\n        transition[Open](move file)\n"},
		{"prefix_collision", "module VaultKit:\n    def invalid(file: Vault::File[Closed]) -> Vault::File[Open]:\n        transition[Open](move file)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", owned+tc.source, AnalyzeOptions{})
			if diagnostics := allDiagnostics(result); !strings.Contains(diagnostics, "private to its owning module") {
				t.Fatal(diagnostics)
			}
		})
	}
	analyzeTreeTestSource(t, "descendant_authority.elisa", owned+`module Vault::Child::Deep:
    def any_operation_name(file: Vault::File[Closed]) -> Vault::File[Open]:
        transition[Open](move file)
`)
	root := protocolGraphPrelude + `module Child:
    def invalid(file: File[Closed]) -> File[Open]:
        transition[Open](move file)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "root_boundary.elisa", root, AnalyzeOptions{})
	if diagnostics := allDiagnostics(result); !strings.Contains(diagnostics, "private to its owning module") {
		t.Fatal(diagnostics)
	}
}

func TestProtocolGraphCanonicalStateOwnerIdentity(t *testing.T) {
	owned := "module Left:\n    " + strings.ReplaceAll(strings.TrimSpace(protocolGraphPrelude), "\n", "\n    ") + "\n\n"
	owned += "module Right:\n    " + strings.ReplaceAll(strings.TrimSpace(protocolGraphPrelude), "\n", "\n    ") + "\n\n"
	result := analyzeTreeTestSource(t, "state_owner_identity.elisa", owned)
	left := result.NamedTypes["Left.File"].(*StructType)
	right := result.NamedTypes["Right.File"].(*StructType)
	if left.GenericParams[0].StateOwner != left.Name || right.GenericParams[0].StateOwner != right.Name {
		t.Fatal("semantic state parameters lost canonical family identity")
	}
	if left.Decl.GenericParams[0].StateOwner != "File" || right.Decl.GenericParams[0].StateOwner != "File" {
		t.Fatal("qualification mutated the source AST")
	}
	leftState := newNamedStateType(left.GenericParams[0].StateOwner, left.NamedStateCases, []string{"Open"})
	rightState := newNamedStateType(right.GenericParams[0].StateOwner, right.NamedStateCases, []string{"Open"})
	if SameType(leftState, rightState) || AssignableTo(leftState, rightState) || CanonicalTypeID(leftState) == CanonicalTypeID(rightState) {
		t.Fatal("equal state spellings in separate families acquired the same identity")
	}
}

func TestProtocolGraphUserTransitionFunctionIsOrdinary(t *testing.T) {
	analyzeTreeTestSource(t, "user_transition.elisa", protocolGraphPrelude+`enum Open:
    Value

def transition[T](file: File[Closed]) -> File[Closed]:
    return move file

def ordinary(file: File[Closed]) -> File[Closed]:
    transition[Open](move file)
`)
}
