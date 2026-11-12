package astscan

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/proofcoordinatescan"
)

func WalkProofCoordinates(root *ast.File, add func(lexer.Pos), seen map[uintptr]bool) {
	proofcoordinatescan.WalkProofCoordinates(root, add, seen)
}
