//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

func TestBoundsOverlayReplacementInvalidatesSpanCache(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "local"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			result := &semantic.Result{}
			g := &llvmGenerator{result: result, sharedResult: shared}
			if shared {
				g.sharedLookups = newSharedCodegenLookups(result)
			}
			state := &functionState{g: g}
			pos := lexer.Pos{File: "synth.elisa", Line: 2, Col: 3, Offset: 8}
			original := &ast.IndexExpr{Position: pos}
			rebuilt := &ast.IndexExpr{Position: pos}
			for _, want := range []bool{true, false, true} {
				g.setResultIndexBoundsProven(original, want)
				if got := state.indexBoundsProven(rebuilt); got != want {
					t.Fatalf("span proof = %v, want %v", got, want)
				}
			}
		})
	}
}
