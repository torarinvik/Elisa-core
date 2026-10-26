//go:build cgo

package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestCompactProofCoordinateWitnessesPreserveExactDistinctStream(t *testing.T) {
	const source = `#line 12 included.elisa
def first(value: i64) -> i64:
    in auto:
        return value

def second(values: darray[i64]) -> bool:
    return forall index in 0..<values.count: values[index] > 0
`
	file := proofCoordinateNamedParse(t, "root.elisa", source)
	before := captureProofReportCoordinateWitnessesBeforeCompactLAB(file)
	after := captureProofReportCoordinateWitnesses(file)
	var wanted, actual []lexer.Pos
	oldCount := 0
	for _, slab := range before.slabs {
		for _, position := range slab {
			oldCount++
			if len(wanted) == 0 || wanted[len(wanted)-1] != position {
				wanted = append(wanted, position)
			}
		}
	}
	for _, slab := range after.slabs {
		actual = append(actual, slab...)
	}
	if !reflect.DeepEqual(actual, wanted) {
		t.Fatal("compaction changed distinct witness tuple/order")
	}
	if len(actual) >= oldCount {
		t.Fatal("real parser fixture did not eliminate duplicate node/field coordinates")
	}
	requests := []proofReportCoordinateRequest{}
	for _, slab := range before.slabs {
		for _, position := range slab {
			requests = append(requests, proofReportCoordinateRequest{position, false}, proofReportCoordinateRequest{position, true})
			position.Offset += 123
			position.EndOffset += 123
			requests = append(requests, proofReportCoordinateRequest{position, false})
		}
	}
	combined := file.Decls[0].Pos()
	end := file.Decls[1].Pos()
	combined.EndLine, combined.EndCol, combined.EndOffset = end.Line, end.Col, end.Offset
	requests = append(requests, proofReportCoordinateRequest{combined, false})
	compareRequestedCoordinateOracle(t, file, nil, requests)
}

func TestCompactProofCoordinateWitnessesKeepConflictingAndSeparatedPositions(t *testing.T) {
	first := lexer.Pos{File: "same.elisa", Line: 7, Col: 3, Offset: 10, EndLine: 7, EndCol: 8, EndOffset: 15}
	conflict := first
	conflict.Offset = 20
	conflict.EndOffset = 25
	separated := lexer.Pos{File: "other.elisa", Line: 2, Col: 1, Offset: 30}
	file := &ast.File{Decls: []ast.Decl{
		&ast.FuncDecl{Name: "first", Position: first},
		&ast.FuncDecl{Name: "separated", Position: separated},
		&ast.FuncDecl{Name: "again", Position: first},
		&ast.FuncDecl{Name: "conflict", Position: conflict},
	}}
	after := captureProofReportCoordinateWitnesses(file)
	var actual []lexer.Pos
	for _, slab := range after.slabs {
		actual = append(actual, slab...)
	}
	wanted := []lexer.Pos{first, separated, first, conflict}
	if !reflect.DeepEqual(actual, wanted) {
		t.Fatalf("nonconsecutive/conflicting authority omitted: %#v", actual)
	}
	compareRequestedCoordinateOracle(t, file, nil, []proofReportCoordinateRequest{{first, false}, {conflict, false}, {separated, false}})
}

// Frozen immediate pre-compaction capture; this is test-only and retains the
// original node.Pos and concrete position-field visit behavior without edits.
func captureProofReportCoordinateWitnessesBeforeCompactLAB(file *ast.File) proofReportCoordinateWitnesses {
	witnesses := proofReportCoordinateWitnesses{}
	addPosition := func(p lexer.Pos) {
		if p.IsZero() {
			return
		}
		if len(witnesses.slabs) == 0 || len(witnesses.slabs[len(witnesses.slabs)-1]) == proofReportCoordinateWitnessSlabSize {
			witnesses.slabs = append(witnesses.slabs, make([]lexer.Pos, 0, proofReportCoordinateWitnessSlabSize))
		}
		last := len(witnesses.slabs) - 1
		witnesses.slabs[last] = append(witnesses.slabs[last], p)
	}
	seen := map[uintptr]bool{}
	var visit func(reflect.Value)
	visit = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		if v.Type() == retainedPosType {
			// Some producers intentionally use only a token start. Preserve that
			// contract; never manufacture an end span during rendering.
			addPosition(v.Interface().(lexer.Pos))
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if v.CanInterface() {
				if node, ok := v.Interface().(ast.Node); ok {
					addPosition(node.Pos())
				}
			}
			visit(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return
			}
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(file))
	return witnesses
}
