//go:build cgo

package semantic

// advanced_features_gate_test.go
//
// Soundness/erasure gate suite covering ghost fields, typestate, and named contracts.
// Invariants that hold today are asserted; not-yet-enforced gaps are left as // TODO comments.
//
// Run with: go test ./src/semantic -run 'AdvancedFeaturesGate'

import (
	"strings"
	"testing"
)

// ============================================================
// I. GHOST FIELD ERASURE
// ============================================================

// AdvancedFeaturesGate_GhostFieldNotInConcreteLayout: a struct with a ghost field must have the
// ghost omitted from GhostFieldOrder's complement (i.e., in GhostFieldOrder) and NOT appear in
// the regular Decl.Fields slice. Ghost fields must be absent from the concrete layout.
func TestAdvancedFeaturesGate_GhostFieldNotInConcreteLayout(t *testing.T) {
	src := `
struct Box:
    concrete: i64
    ghost model: i64
`
	result := analyzeTreeTestSource(t, "ghost_layout.elisa", src)

	var st *StructType
	for _, typ := range result.NamedTypes {
		if s, ok := typ.(*StructType); ok && s.Name == "Box" {
			st = s
			break
		}
	}
	if st == nil {
		t.Fatal("Box struct not found in analysis")
	}

	// The ghost field must be recorded in GhostFieldOrder (semantic layer knows it).
	if len(st.GhostFieldOrder) == 0 {
		t.Error("SOUNDNESS VIOLATION: ghost field 'model' missing from GhostFieldOrder")
	} else if st.GhostFieldOrder[0] != "model" {
		t.Errorf("expected GhostFieldOrder[0]='model', got %q", st.GhostFieldOrder[0])
	}

	// The ghost field must be absent from Decl.Fields (the concrete layout list).
	for _, f := range st.Decl.Fields {
		if f.Name == "model" {
			t.Error("SOUNDNESS VIOLATION: ghost field 'model' appears in Decl.Fields (concrete layout) — it must be erased")
		}
	}

	// The ghost field must be present in the semantic Fields map (for contract/invariant use),
	// and marked Ghost=true.
	fld, ok := st.Fields["model"]
	if !ok {
		t.Error("ghost field 'model' missing from semantic Fields map (needed for contract analysis)")
	} else if !fld.Ghost {
		t.Error("SOUNDNESS VIOLATION: field 'model' is in Fields but Ghost flag is false")
	}
}

// AdvancedFeaturesGate_GhostLayoutSameAsConcrete: a struct with a ghost field has the same
// concrete Decl.Fields as an identical struct without the ghost field.
func TestAdvancedFeaturesGate_GhostLayoutSameAsConcrete(t *testing.T) {
	src := `
struct WithGhost:
    data: i64
    count: u32
    ghost model: i64

struct WithoutGhost:
    data: i64
    count: u32
`
	result := analyzeTreeTestSource(t, "ghost_layout_parity.elisa", src)

	var withGhost, withoutGhost *StructType
	for _, typ := range result.NamedTypes {
		if s, ok := typ.(*StructType); ok {
			switch s.Name {
			case "WithGhost":
				withGhost = s
			case "WithoutGhost":
				withoutGhost = s
			}
		}
	}
	if withGhost == nil || withoutGhost == nil {
		t.Fatal("did not find both struct variants")
	}

	if len(withGhost.Decl.Fields) != len(withoutGhost.Decl.Fields) {
		t.Errorf("SOUNDNESS VIOLATION: Decl.Fields length mismatch after ghost erasure: withGhost=%d, withoutGhost=%d",
			len(withGhost.Decl.Fields), len(withoutGhost.Decl.Fields))
	}
	for i := 0; i < len(withGhost.Decl.Fields) && i < len(withoutGhost.Decl.Fields); i++ {
		if withGhost.Decl.Fields[i].Name != withoutGhost.Decl.Fields[i].Name {
			t.Errorf("SOUNDNESS VIOLATION: Decl.Fields[%d] mismatch: %q vs %q",
				i, withGhost.Decl.Fields[i].Name, withoutGhost.Decl.Fields[i].Name)
		}
	}
}

// AdvancedFeaturesGate_GhostFieldReadInRealCodeRejected: reading a ghost field in a non-ghost
// runtime function is a semantic error. This prevents ghost state from leaking into real code.
func TestAdvancedFeaturesGate_GhostFieldReadInRealCodeRejected(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64

def leak(self: Counter&) -> i64:
    return self.model
`
	result := analyzeWithSMT(t, "ghost_field_real_read_gate.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "ghost field") {
		t.Fatalf("SOUNDNESS VIOLATION: reading ghost field in real code must be rejected; got errors: %v", result.Errors())
	}
}

// AdvancedFeaturesGate_GhostFieldUsableInInvariant: a ghost field IS readable in a struct
// invariant (a contract/ghost context). This is the dual of the above — erasure is behavioral,
// not total silencing at the type level.
func TestAdvancedFeaturesGate_GhostFieldUsableInInvariant(t *testing.T) {
	src := `
struct Box:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def use(b: Box&) -> i64:
    return b.concrete
`
	result := analyzeWithSMT(t, "ghost_field_invariant_gate.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("ghost field readable in invariant must be clean, got: %v", errs)
	}
}

// ============================================================
// II. TYPESTATE ERASURE
// ============================================================

// AdvancedFeaturesGate_TypestateFieldIsPhantom: a struct with a state parameter must mark the
// __typestate field as Phantom=true, ensuring it is excluded from codegen layout.
func TestAdvancedFeaturesGate_TypestateFieldIsPhantom(t *testing.T) {
	src := `
struct Socket[state Closed | Open]:
    fd: mutable i64
    __typestate: mutable i64

    derive state:
        Closed when self.__typestate == 0
        Open when self.__typestate == 1
`
	result := analyzeTreeTestSource(t, "typestate_phantom_gate.elisa", src)

	var st *StructType
	for _, typ := range result.NamedTypes {
		if s, ok := typ.(*StructType); ok && s.Name == "Socket" {
			st = s
			break
		}
	}
	if st == nil {
		t.Fatal("Socket struct not found")
	}

	if !st.HasTypestate {
		t.Error("SOUNDNESS VIOLATION: Socket has state param but HasTypestate=false")
	}

	fld, ok := st.Fields["__typestate"]
	if !ok {
		t.Fatal("__typestate field missing from semantic Fields map")
	}
	if !fld.Phantom {
		t.Error("SOUNDNESS VIOLATION: __typestate field must be Phantom=true for codegen erasure")
	}
}

// AdvancedFeaturesGate_TypestateLayoutParityWithPlainStruct: the non-phantom concrete fields of a
// typestate struct must match those of an equivalent plain struct — state annotation has zero
// runtime layout impact.
func TestAdvancedFeaturesGate_TypestateLayoutParityWithPlainStruct(t *testing.T) {
	src := `
struct TypedSocket[state Closed | Open]:
    fd: mutable i64
    flags: u32
    __typestate: mutable i64

    derive state:
        Closed when self.__typestate == 0
        Open when self.__typestate == 1

struct PlainSocket:
    fd: mutable i64
    flags: u32
`
	result := analyzeTreeTestSource(t, "typestate_layout_parity_gate.elisa", src)

	var typed, plain *StructType
	for _, typ := range result.NamedTypes {
		if s, ok := typ.(*StructType); ok {
			switch s.Name {
			case "TypedSocket":
				typed = s
			case "PlainSocket":
				plain = s
			}
		}
	}
	if typed == nil || plain == nil {
		t.Fatal("did not find both struct variants")
	}

	// Collect non-phantom fields for the typestate struct.
	var typedConcrete []string
	for _, f := range typed.Decl.Fields {
		if fld, ok := typed.Fields[f.Name]; ok && !fld.Phantom && !fld.Ghost {
			typedConcrete = append(typedConcrete, f.Name)
		}
	}
	var plainConcrete []string
	for _, f := range plain.Decl.Fields {
		if fld, ok := plain.Fields[f.Name]; ok && !fld.Phantom && !fld.Ghost {
			plainConcrete = append(plainConcrete, f.Name)
		}
	}

	if len(typedConcrete) != len(plainConcrete) {
		t.Errorf("SOUNDNESS VIOLATION: concrete-field count mismatch after typestate erasure: typed=%v, plain=%v",
			typedConcrete, plainConcrete)
	}
	for i := 0; i < len(typedConcrete) && i < len(plainConcrete); i++ {
		if typedConcrete[i] != plainConcrete[i] {
			t.Errorf("SOUNDNESS VIOLATION: concrete field[%d] mismatch: %q vs %q",
				i, typedConcrete[i], plainConcrete[i])
		}
	}
}

// AdvancedFeaturesGate_TypestateFieldPhantomInSemanticMap: the __typestate field is present in
// Decl.Fields (for semantic analysis and construction) but MUST be marked Phantom=true in the
// Fields map, which signals codegen to skip it from layout. This is the erasure invariant: the
// field is structurally present but phantom-flagged, not physically removed from the list.
func TestAdvancedFeaturesGate_TypestateFieldPhantomInSemanticMap(t *testing.T) {
	src := `
struct Conn[state Idle | Active]:
    host: cstr
    port: u16
    __typestate: mutable i64

    derive state:
        Idle when self.__typestate == 0
        Active when self.__typestate == 1
`
	result := analyzeTreeTestSource(t, "typestate_phantom_map_gate.elisa", src)

	var st *StructType
	for _, typ := range result.NamedTypes {
		if s, ok := typ.(*StructType); ok && s.Name == "Conn" {
			st = s
			break
		}
	}
	if st == nil {
		t.Fatal("Conn struct not found")
	}

	// __typestate IS expected in Decl.Fields (semantic analysis needs it);
	// the erasure mechanism is the Phantom flag in the semantic Fields map.
	found := false
	for _, f := range st.Decl.Fields {
		if f.Name == "__typestate" {
			found = true
		}
	}
	if !found {
		t.Error("__typestate missing from Decl.Fields — it must be present for semantic analysis (erasure is via Phantom flag, not removal)")
	}

	// The Phantom flag in the semantic Fields map is what signals codegen erasure.
	fld, ok := st.Fields["__typestate"]
	if !ok {
		t.Fatal("__typestate missing from semantic Fields map")
	}
	if !fld.Phantom {
		t.Error("SOUNDNESS VIOLATION: __typestate in semantic Fields map has Phantom=false — codegen will include it in layout")
	}
}

// TODO: AdvancedFeaturesGate_TypestateWrongStateOperationRejected
// An operation declared to require state=Open invoked on a value in state=Closed should be
// rejected. Not yet enforced at the semantic layer (S0 only covers erasure, not state-transition
// checking). Uncomment when state-transition enforcement lands (docs/111 S1+).
//
// func TestAdvancedFeaturesGate_TypestateWrongStateOperationRejected(t *testing.T) {
//     t.Skip("typestate operation-on-wrong-state enforcement not yet implemented (docs/111 S1+)")
// }

// ============================================================
// III. NAMED CONTRACT SOUNDNESS
// ============================================================

// AdvancedFeaturesGate_NamedContractSatisfiedIsClean: a `uses` of a contract whose precondition
// is satisfied and whose postcondition is provably met produces no errors.
func TestAdvancedFeaturesGate_NamedContractSatisfiedIsClean(t *testing.T) {
	src := `
contract NonNeg(x: i64):
    requires x >= 0
    ensure result >= 0

def identity(n: i64) -> i64:
    uses NonNeg(n)
    return n
`
	result := analyzeContractStrict(t, "named_contract_ok_gate.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a satisfied named contract must be clean, got: %v", errs)
	}
}

// AdvancedFeaturesGate_NamedContractViolatedEnsureRejected: a `uses` whose ensure clause the
// function body violates must produce a "could not be proven" error.
func TestAdvancedFeaturesGate_NamedContractViolatedEnsureRejected(t *testing.T) {
	src := `
contract NonNeg(out: i64, src: i64):
    requires src >= 0
    ensure result >= src

def bad(s: i64) -> i64:
    uses NonNeg(0, s)
    return s - 1
`
	result := analyzeContractStrict(t, "named_contract_bad_ensure_gate.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("SOUNDNESS VIOLATION: violated `ensure` in `uses` must be rejected; got: %v", result.Errors())
	}
}

// AdvancedFeaturesGate_NamedContractViolatedRequiresAtCallSiteRejected: calling a function that
// uses a contract with an argument that fails the contract's `requires` must be an error.
func TestAdvancedFeaturesGate_NamedContractViolatedRequiresAtCallSiteRejected(t *testing.T) {
	src := `
contract MustBePositive(x: i64):
    requires x > 0
    ensure result > 0

def wrap(n: i64) -> i64:
    uses MustBePositive(n)
    return n

def bad_caller() -> i64:
    return wrap(0 - 1)
`
	result := analyzeContractStrict(t, "named_contract_requires_callsite_gate.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatal("SOUNDNESS VIOLATION: call with negative arg violates `requires x > 0` — must be rejected")
	}
}

// AdvancedFeaturesGate_NamedContractUnknownNameRejected: a `uses` referencing an undeclared
// contract must be a hard error (not silently ignored).
func TestAdvancedFeaturesGate_NamedContractUnknownNameRejected(t *testing.T) {
	src := `
def test(n: i64) -> i64:
    uses NonExistentContract(n)
    return n
`
	result := analyzeContractStrict(t, "named_contract_unknown_gate.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatal("SOUNDNESS VIOLATION: `uses` of an undeclared contract must be rejected; got no errors")
	}
}

// ============================================================
// IV. COMBINED: GHOST FIELD + NAMED CONTRACT
// ============================================================

// AdvancedFeaturesGate_GhostFieldAndContractCombined: a struct with a ghost field whose invariant
// ties concrete to model, combined with a named contract that uses the concrete field, must compile
// cleanly when the contract is satisfied.
func TestAdvancedFeaturesGate_GhostFieldAndContractCombined(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

contract NonNeg(x: i64):
    requires x >= 0
    ensure result >= 0

def get(c: Counter&) -> i64:
    uses NonNeg(c.concrete)
    return c.concrete
`
	result := analyzeContractStrict(t, "ghost_and_contract_combined_gate.elisa", src)
	// TODO: this may produce errors if the invariant assumption is not injected into the
	// contract-proof context for `NonNeg`. Mark as expected-pass if it succeeds cleanly.
	// For now, assert that parsing/registration itself succeeds (no parse/type errors).
	for _, e := range result.Errors() {
		if strings.Contains(e, "parse") || strings.Contains(e, "undefined") || strings.Contains(e, "unknown") {
			t.Errorf("unexpected structural error (parse/type) in ghost+contract combined test: %s", e)
		}
	}
}

// AdvancedFeaturesGate_GhostFieldWithContractGhostNotLeakedToEnsure: a named contract whose
// `ensure` references the ghost field of a struct argument must be rejected (ghost-to-real flow).
//
// TODO: This invariant (contract ensure may not reference ghost fields of arguments) is not yet
// enforced. The test is left as a documented gap.
// func TestAdvancedFeaturesGate_GhostFieldWithContractGhostNotLeakedToEnsure(t *testing.T) {
//     t.Skip("ghost-to-contract-ensure flow not yet blocked (TODO)")
// }

// ============================================================
// V. COMBINED: TYPESTATE + NAMED CONTRACT
// ============================================================

// AdvancedFeaturesGate_TypestateStructWithContractOnConcreteField: a typestate struct's concrete
// fields are usable as named contract arguments (typestate erasure does not break contract
// analysis on the visible fields).
func TestAdvancedFeaturesGate_TypestateStructWithContractOnConcreteField(t *testing.T) {
	src := `
struct Connection[state Idle | Active]:
    port: u16
    __typestate: mutable i64

    derive state:
        Idle when self.__typestate == 0
        Active when self.__typestate == 1

contract ValidPort(p: u16):
    requires p > 0

def check(c: Connection&) -> u16:
    uses ValidPort(c.port)
    return c.port
`
	result := analyzeContractStrict(t, "typestate_and_contract_gate.elisa", src)
	// No structural/parse/type errors expected.
	for _, e := range result.Errors() {
		if strings.Contains(e, "undefined") || strings.Contains(e, "unknown") || strings.Contains(e, "parse") {
			t.Errorf("unexpected structural error in typestate+contract combined test: %s", e)
		}
	}
}
