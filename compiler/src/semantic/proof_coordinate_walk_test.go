//go:build cgo

package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Unknown Node implementations must keep both their actual Pos() and every
// reflected Pos field. Embedding supplies the Expr marker, not Pos authority.
type proofCoordinateOpaqueExpr struct {
	ast.Expr
	Position lexer.Pos
	Payload  any
	hidden   string
}

func (p *proofCoordinateOpaqueExpr) Pos() lexer.Pos {
	position := p.Position
	position.Col += 7
	position.Offset += 7
	return position
}

type proofCoordinateNamedScalar string
type proofCoordinateOpaquePayload struct {
	ScalarFirst *string
	ScalarAlias *string
	Position    lexer.Pos
	Child       ast.Expr
	Next        *proofCoordinateOpaquePayload
	Scalar      proofCoordinateNamedScalar
	Array       [1]lexer.Pos
	Map         map[string]lexer.Pos
	Bytes       []byte
}

func assertProofCoordinateTypedStreamEqual(t *testing.T, file *ast.File) {
	t.Helper()
	// Frozen reflective capture includes adjacent duplicate coordinates. Compare
	// exact tuple/order after precisely the already-shipped compaction operation.
	frozen := captureProofReportCoordinateWitnessesBeforeCompactLAB(file)
	var expected, actual []lexer.Pos
	for _, slab := range frozen.slabs {
		for _, position := range slab {
			if len(expected) == 0 || expected[len(expected)-1] != position {
				expected = append(expected, position)
			}
		}
	}
	current := captureProofReportCoordinateWitnesses(file)
	for _, slab := range current.slabs {
		actual = append(actual, slab...)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("typed capture changed exact witness sequence: got=%#v want=%#v", actual, expected)
	}
	requests := []proofReportCoordinateRequest{}
	for _, position := range expected {
		requests = append(requests, proofReportCoordinateRequest{position, false})
		position.Offset += 123
		position.EndOffset += 123
		requests = append(requests, proofReportCoordinateRequest{position, false})
	}
	compareRequestedCoordinateOracle(t, file, nil, requests)
}

func TestProofCoordinateTypedStreamPreservesAliasesAndUnknownNodeFallback(t *testing.T) {
	leaf := &ast.IntLit{Position: lexer.Pos{File: "fallback.elisa", Line: 2, Col: 3, Offset: 23}, Value: "7"}
	ignored := lexer.Pos{File: "ignored-map-array.elisa", Line: 99, Col: 1, Offset: 999}
	guardedLeaf := &ast.IntLit{Position: lexer.Pos{File: "scalar-pointer-guarded.elisa", Line: 4, Col: 2, Offset: 42}, Value: "8"}
	payload := &proofCoordinateOpaquePayload{ScalarFirst: &guardedLeaf.Position.File, ScalarAlias: &guardedLeaf.Position.File, Position: lexer.Pos{File: "payload.elisa", Line: 3, Col: 2, Offset: 32}, Child: guardedLeaf, Scalar: proofCoordinateNamedScalar("__auto_42"), Array: [1]lexer.Pos{ignored}, Map: map[string]lexer.Pos{"ignored": ignored}, Bytes: []byte{1, 2, 3}}
	payload.Next = payload
	unknown := &proofCoordinateOpaqueExpr{Expr: leaf, Position: lexer.Pos{File: "unknown.elisa", Line: 1, Col: 1, Offset: 11}, Payload: payload, hidden: "scalar-only"}
	call := &ast.CallExpr{Position: lexer.Pos{File: "call.elisa", Line: 5, Col: 2, Offset: 52}, Func: unknown, Args: []ast.Expr{leaf, unknown}, ResolvedArgs: []ast.Expr{unknown, leaf}}
	file := &ast.File{Decls: []ast.Decl{&ast.FuncDecl{Name: "first", Body: []ast.Stmt{&ast.ReturnStmt{Value: call}}}, &ast.FuncDecl{Name: "second", Body: []ast.Stmt{&ast.ReturnStmt{Value: unknown}}}}}
	assertProofCoordinateTypedStreamEqual(t, file)
	for _, slab := range captureProofReportCoordinateWitnesses(file).slabs {
		for _, position := range slab {
			if position.File == guardedLeaf.Position.File {
				t.Fatal("scalar pointer aliases lost original cross-type pointer visitation guard")
			}
			if position.File == ignored.File {
				t.Fatal("typed capture traversed original map/array leaves")
			}
		}
	}
}

func TestProofCoordinateTypedStreamMatchesIndependentParserIncludesAndProducerShapes(t *testing.T) {
	source := `#line 6 first.elisa
def moved() -> i64:
    in auto:
        return 3
#line 6 second.elisa
def values(items: darray[i64]) -> bool:
    return forall index in 0..<items.count: items[index] > 0
`
	file := proofCoordinateNamedParse(t, "root.elisa", source)
	assertProofCoordinateTypedStreamEqual(t, file)
	assertProofCoordinateTypedStreamEqual(t, proofCoordinateNamedParse(t, "root.elisa", "\n"+source))
}
