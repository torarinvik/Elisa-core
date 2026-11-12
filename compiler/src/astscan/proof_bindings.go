package astscan

import (
	"elisacore/src/ast"
	"elisacore/src/proofbindingscan"
	"reflect"
)

const (
	BindingVisitNode         = proofbindingscan.BindingVisitNode
	BindingVisitStringField  = proofbindingscan.BindingVisitStringField
	BindingVisitString       = proofbindingscan.BindingVisitString
	BindingVisitPositionFile = proofbindingscan.BindingVisitPositionFile
)

func WalkProofBindings(root *ast.File, visit func(any, reflect.Type, ast.Decl, uint8, string, string, bool), seen map[uintptr]bool) {
	proofbindingscan.WalkProofBindings(root, visit, seen)
}

func ProofBindingPositionSchemaMatches(typ reflect.Type) bool {
	return proofbindingscan.ProofBindingPositionSchemaMatches(typ)
}
