package semantic

import (
	"strings"
	"testing"
)

const storeFlowPositionCopyBody = `
struct Expr:
    at: Position
    text: sview
struct State:
    expr: Expr
struct Diagnostic:
    at: Position
    name: sview
struct Table:
    rows: mutable darray[Diagnostic]
    held: mutable State&?
def expr_pos(expr: Expr) -> Position:
    return expr.at
def record(expr: Expr, table: lmut Table) -> void:
    can Memory.Allocate, Abort.Panic:
        at: Position = expr_pos(expr)
        table.rows <- table.rows.push(Diagnostic{at: at, name: "literal"})
def walk(state: mutable State&, table: lmut Table) -> void:
    can Memory.Allocate, Abort.Panic:
        table <- record(state.expr, table)
def check(table: lmut Table) -> void:
    marker: usize = 1
    state: mutable State = State{expr: Expr{at: POSITION_VALUE, text: "literal"}}
    can Memory.Allocate, Abort.Panic:
        table <- walk(&state, table)
def main() -> i32:
    return 0
`

// The destination can hold a State reference, but record only copies a scalar
// position. The position must not make the local State address escape.
func TestStoreFlowSummaryCopiesResolvedScalarPositions(t *testing.T) {
	cases := []struct{ name, declaration, value, positionType string }{
		{"struct", "struct Position:\n    line: usize\n", "Position{line: 1}", "Position"},
		{"qualified", "module Coordinates:\n    public:\n        struct Pos:\n            line: usize\n", "Coordinates::Pos{line: 1}", "Coordinates::Pos"},
		{"alias", "module Coordinates:\n    public:\n        struct Pos:\n            line: usize\ntype Position = Coordinates::Pos\n", "Position{line: 1}", "Position"},
		{"enum", "enum Position:\n    At(line: usize)\n", "Position.At(1)", "Position"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.ReplaceAll(storeFlowPositionCopyBody, "Position", tc.positionType)
			body = strings.ReplaceAll(body, "POSITION_VALUE", tc.value)
			result := analyzeFunctionAnalysisTestSource(t, "position_copy_"+tc.name+".elisa", tc.declaration+body)
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("scalar position copy must be accepted: %s", strings.Join(errs, "\n"))
			}
		})
	}
}

func TestStoreFlowSummaryRetainsReferenceAggregateResults(t *testing.T) {
	cases := []struct{ name, declaration, value string }{
		{"nested_struct", "struct Inner:\n    borrow: usize&\nstruct Position:\n    line: usize\n    inner: Inner\n", "Position{line: 1, inner: Inner{borrow: &marker}}"},
		{"enum_variant", "enum Position:\n    At(line: usize)\n    Borrow(value: usize&)\n", "Position.Borrow(&marker)"},
		{"alias", "struct Inner:\n    borrow: usize&\ntype Position = Inner\n", "Position{borrow: &marker}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.ReplaceAll(storeFlowPositionCopyBody, "POSITION_VALUE", tc.value)
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "position_reference_"+tc.name+".elisa", tc.declaration+body, AnalyzeOptions{})
			if errs := strings.Join(result.Errors(), "\n"); !strings.Contains(errs, "dangles once the function returns") {
				t.Fatalf("reference aggregate must retain its dependency: %s", errs)
			}
		})
	}
}

func TestStoreFlowPointerFreeReturnConservativeBoundaries(t *testing.T) {
	cycle := &StructType{Name: "Cycle", Fields: map[string]Field{}}
	cycle.Fields["self"] = Field{Type: cycle}
	for name, typ := range map[string]Type{
		"cycle":             cycle,
		"unresolved_struct": &StructType{Name: "Pending"},
		"generic":           &TypeParamType{Name: "T"},
		"container":         &DArrayType{Elem: &BuiltinType{Name: "usize"}},
		"packed_enum":       &EnumType{Packed: true, Variants: []*EnumVariant{{Name: "At", Payload: []Type{&BuiltinType{Name: "usize"}}}}},
		"reference":         &RefType{Elem: &BuiltinType{Name: "usize"}},
	} {
		if storeFlowPointerFreeReturn(typ, map[Type]bool{}) {
			t.Errorf("%s must conservatively retain return flow", name)
		}
	}
}

func TestStoreFlowCanExprPreservesReturnDependencies(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		body := strings.ReplaceAll(storeFlowPositionCopyBody, "POSITION_VALUE", "Position{line: 1}")
		body = "struct Position:\n    line: usize\n" + body
		returned := "\"literal\""
		if borrowed {
			returned = "expr.text"
		}
		body = strings.Replace(body, "def expr_pos(expr: Expr) -> Position:\n    return expr.at", "def expr_pos(expr: Expr) -> sview:\n    return "+returned, 1)
		body = strings.Replace(body, "at: Position = expr_pos(expr)", "name: sview = expr_pos(expr) can Memory.Allocate", 1)
		body = strings.Replace(body, "Diagnostic{at: at, name: \"literal\"}", "Diagnostic{at: Position{line: 1}, name: name}", 1)
		if borrowed {
			body = strings.Replace(body, "text: \"literal\"", "text: local_text", 1)
			body = strings.Replace(body, "marker: usize = 1", "marker: usize = 1\n    storage: darray[u8] = [65.u8()]\n    local_text: sview = storage.as_sview()", 1)
		}
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "can_return_flow.elisa", body, AnalyzeOptions{})
		errors := strings.Join(result.Errors(), "\n")
		if borrowed && !strings.Contains(errors, "dangles once the function returns") {
			t.Fatalf("borrowed return must retain dependency: %s", errors)
		}
		if !borrowed && errors != "" {
			t.Fatalf("literal return must remain independent: %s", errors)
		}
	}
}
