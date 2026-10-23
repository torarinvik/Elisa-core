//go:build cgo

package backend

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

func TestPartitionedIRStartsChangedUnitFirst(t *testing.T) {
	const parts = 64
	name := "edited"
	for i := 0; partitionOwner(name, parts) == 0; i++ {
		name = fmt.Sprintf("edited%d", i)
	}
	before := fmt.Sprintf("def %s(x: i64) -> i64:\n    return x + 1\n\ndef main() -> i64:\n    return %s(1)\n", name, name)
	parse := func(source string) *ast.File {
		l := lexer.New("priority.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("priority.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("fixture parse failed")
		}
		return f
	}
	result, state := semantic.AnalyzeRetained(parse(before), parse(before), []byte(before), semantic.AnalyzeOptions{})
	initial := partitionedIRWorkOrder(result, parts)
	for i, part := range initial {
		if i != part {
			t.Fatal("cold analysis reordered units")
		}
	}
	after := strings.Replace(before, "return x + 1", "return x + 2", 1)
	result, err := state.Update(parse(after), parse(after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	hints := result.CodegenFunctionPriorities()
	if !reflect.DeepEqual(hints, []string{name}) {
		t.Fatalf("missing changed function hint: %v", hints)
	}
	hints[0] = "changed by caller"
	if result.CodegenFunctionPriorities()[0] != name {
		t.Fatal("caller mutated retained hint state")
	}
	order := partitionedIRWorkOrder(result, parts)
	if order[0] != partitionOwner(name, parts) {
		t.Fatal("likely miss was not queued first")
	}
	seen := map[int]bool{}
	for _, part := range order {
		if part < 0 || part >= parts || seen[part] {
			t.Fatal("unit lost or duplicated")
		}
		seen[part] = true
	}
	if len(seen) != parts {
		t.Fatal("priority skipped unmodified units")
	}
}
