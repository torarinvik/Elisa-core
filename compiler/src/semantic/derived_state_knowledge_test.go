//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

const positiveOnlyPrelude = `struct PositiveOnly[state Positive]:
	health: mutable i64
	tag: i64
	derive state:
		Positive when self.health > 0

`

func TestDerivedSingleStateUnknownEvidenceRejects(t *testing.T) {
	for name, body := range map[string]string{
		"straight_line": "p.health <- value",
		"while_exit":    "while again:\n\t\tp.health <- value",
		"for_exit":      "for i in 0..<3 |p|:\n\t\tp.health <- value",
		"break_exit":    "while again:\n\t\tp.health <- value\n\t\tbreak",
		"continue_exit": "while again:\n\t\tp.health <- value\n\t\tcontinue",
		"branch_join":   "if again:\n\t\tp.health <- value",
		"unknown_call":  "change(p, value)",
	} {
		t.Run(name, func(t *testing.T) {
			src := positiveOnlyPrelude + "extern change(p: mutable PositiveOnly[Positive]&, value: i64) -> void\n\ndef invalid(p: mutable PositiveOnly[Positive], again: bool, value: i64) -> PositiveOnly[Positive]:\n\t" + body + "\n\treturn p{tag = 1}\n"
			result := analyzeDerivedStatePrecision(t, "unknown_state_"+name+".elisa", src)
			if len(result.Errors()) == 0 {
				t.Fatal("possible single state was accepted as predicate evidence")
			}
		})
	}
}

func TestDerivedSingleStateKnowledgeCanRecover(t *testing.T) {
	for name, body := range map[string]string{
		"literal_recovery":           "p.health <- value\n\tp.health <- 1",
		"unknown_loop_then_recovery": "while again:\n\t\tp.health <- value\n\tp.health <- 1",
		"empty_loop":                 "while false:\n\t\tp.health <- value",
		"nonempty_literal_loop":      "for i in 0..<3 |p|:\n\t\tp.health <- 1",
	} {
		t.Run(name, func(t *testing.T) {
			src := positiveOnlyPrelude + "def valid(p: mutable PositiveOnly[Positive], again: bool, value: i64) -> PositiveOnly[Positive]:\n\t" + body + "\n\treturn p{tag = 1}\n"
			result := analyzeDerivedStatePrecision(t, "recover_state_"+name+".elisa", src)
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("established/recovered predicate rejected: %v", errs)
			}
		})
	}
}

func TestDerivedExclusionAloneDoesNotProveRemainingPredicate(t *testing.T) {
	src := `struct Sign[state Positive | Negative]:
	value: mutable i64
	derive state:
		Positive when self.value > 0
		Negative when self.value < 0

def invalid(s: mutable Sign[Positive], value: i64) -> Sign[Positive]:
	requires value >= 0
	s.value <- value
	return s{}
`
	result := analyzeDerivedStatePrecision(t, "nonexhaustive_state.elisa", src)
	if errs := result.Errors(); len(errs) == 0 {
		t.Fatal("excluding Negative manufactured Positive despite possible zero")
	}
}

func TestDerivedUnknownStateCannotBeInferredOrAnnotatedCopy(t *testing.T) {
	for _, copy := range []string{"copy = p{tag = 1}", "copy: PositiveOnly[Positive] = p{tag = 1}"} {
		src := positiveOnlyPrelude + "def invalid(p: mutable PositiveOnly[Positive], value: i64) -> PositiveOnly[Positive]:\n\tp.health <- value\n\t" + copy + "\n\treturn copy\n"
		if errs := analyzeDerivedStatePrecision(t, "unknown_copy.elisa", src).Errors(); len(errs) == 0 {
			t.Fatal("copy erased unknown predicate evidence")
		}
	}
}

func TestDerivedUnknownDoesNotBecomeEntryHypothesis(t *testing.T) {
	src := positiveOnlyPrelude + `def invalid(p: mutable PositiveOnly[Positive], value: i64) -> PositiveOnly[Positive]:
	p.health <- value
	p.health <- p.health
	return p{tag = 1}
`
	result := analyzeDerivedStatePrecision(t, "unknown_entry_hypothesis.elisa", src)
	if !strings.Contains(allDiagnostics(result), "unknown predicate evidence") {
		t.Fatalf("unknown state reused as an entry hypothesis: %s", allDiagnostics(result))
	}
}

func TestNamedStateKnowledgeParticipatesInIdentityAndJoins(t *testing.T) {
	known := newNamedStateType("Only", []string{"Positive"}, []string{"Positive"})
	unknown := withNamedStateEvidence(known, true)
	if SameType(known, unknown) || AssignableTo(known, unknown) || matchTypePattern(known, unknown) {
		t.Fatal("unknown evidence satisfied a known state type")
	}
	if !AssignableTo(unknown, known) || namedStateEvidenceUnknown(known) {
		t.Fatal("knowledge weakening mutated or rejected the known atom")
	}
	for _, pair := range [][2]Type{{known, unknown}, {unknown, known}, {unknown, unknown}} {
		joined := mergeNamedStateTypes(pair[0], pair[1], []string{"Positive"})
		if !namedStateEvidenceUnknown(joined) || AssignableTo(known, joined) {
			t.Fatal("join manufactured single-state knowledge")
		}
	}
}

func TestDerivedFamilyCoverageCertificateRejectsNaNAndGaps(t *testing.T) {
	for _, test := range []struct {
		name, scalar string
		second       lexer.TokenKind
		want         bool
	}{
		{"integer_partition", "i64", lexer.TOKEN_LTEQ, true},
		{"unsigned_partition", "u64", lexer.TOKEN_LTEQ, true},
		{"float_nan_gap", "f64", lexer.TOKEN_LTEQ, false},
		{"zero_gap", "i64", lexer.TOKEN_LT, false},
		{"overlap", "i64", lexer.TOKEN_GTEQ, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			condition := func(op lexer.TokenKind) ast.Expr {
				return &ast.BinaryExpr{Op: op, Left: &ast.FieldExpr{Object: &ast.Ident{Name: "self"}, Field: "value"}, Right: &ast.IntLit{Value: "0"}}
			}
			base := &StructType{Name: "Sign", NamedStateCases: []string{"A", "B"},
				Fields:        map[string]Field{"value": {Type: &BuiltinType{Name: test.scalar}}},
				DerivedStates: []StructDerivedState{{Name: "A", Condition: condition(lexer.TOKEN_GT)}, {Name: "B", Condition: condition(test.second)}},
			}
			if got := derivedStateFamilyTotal(base); got != test.want {
				t.Fatalf("coverage = %v, want %v", got, test.want)
			}
			if namedStateEvidenceUnknown(unknownNamedStateType(base)) == test.want {
				t.Fatal("widening did not respect the checked coverage certificate")
			}
		})
	}
}
