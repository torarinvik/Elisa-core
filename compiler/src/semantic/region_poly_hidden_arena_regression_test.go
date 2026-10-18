package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func TestRegionPolyHiddenArenaStoreToIndependentDeclaredTargets(t *testing.T) {
	source := `def make_row(seed: i64) -> darray[i64]:
    row: mutable darray[i64] = []
    row.push(seed)
    return row

def append_row[@owner, @other](rows: mutable darray[darray[i64]]& @owner, other: mutable darray[darray[i64]]& @other, seed: i64) -> darray[i64]:
    row: darray[i64] = make_row(seed)
    rows.push(row)
    other.push(row)
    return row
	`
	lexer := lexer.New("region_poly_hidden_arena_independent_targets.elisa", []byte(source))
	tokens := lexer.Tokenize()
	if errs := lexer.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	parser := parser.New(tokens)
	file := parser.ParseFile("region_poly_hidden_arena_independent_targets.elisa")
	if errs := parser.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	result := Analyze(file)
	if !regionPolymorphicFuncType(t, result, "make_row").RegionPolymorphic {
		t.Fatal("make_row must be region-polymorphic")
	}
	if !regionPolymorphicFuncType(t, result, "append_row").RegionPolymorphic {
		t.Fatal("append_row must exercise the hidden caller arena")
	}
	if ambient := testFuncDeclByName(t, file, "append_row").AmbientGrownContainerRegion; ambient != "" {
		t.Fatalf("two distinct explicit destinations must not select an ambient target, got %q", ambient)
	}
	errs := result.Errors()
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "region_poly_hidden_arena_independent_targets.elisa:8:") ||
		!strings.Contains(joined, "region_poly_hidden_arena_independent_targets.elisa:9:") ||
		strings.Count(joined, "stored into longer-lived region") < 2 {
		t.Fatalf("stores into both independently supplied destinations must be rejected without an identity proof, got:\n%s", joined)
	}
}
