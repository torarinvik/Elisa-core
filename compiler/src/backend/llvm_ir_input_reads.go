//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// functionBodyForInputRead is the only direct FuncDecl.Body read in lowering.
// The recorder is optional and observational; exact generated bitcode remains
// the existing cache authority. It never suppresses analysis or lowering.
func functionBodyForInputRead(decl *ast.FuncDecl, recorder *irInputReadRecorder, canonical *ast.FuncDecl, use string) []ast.Stmt {
	if recorder != nil {
		recorder.body(decl, canonical, use)
	}
	return decl.Body
}

func (g *llvmGenerator) functionBodyForInputRead(decl *ast.FuncDecl, use string) []ast.Stmt {
	canonical := decl
	if g.inputReads != nil && g.result != nil {
		canonical = g.result.CanonicalCodegenFunction(decl)
	}
	return functionBodyForInputRead(decl, g.inputReads, canonical, use)
}

func (g *llvmGenerator) lookupGlobalForInputRead(name, use string) (*semantic.Symbol, bool) {
	return g.codegenReadView().LookupGlobalSymbol(name, use)
}

func (g *llvmGenerator) specializeFunctionForInputRead(decl *ast.FuncDecl, args []semantic.Type) (*semantic.SpecializedFunction, error) {
	if g.inputReads == nil {
		return g.result.SpecializeFunction(decl, args)
	}
	_ = g.functionBodyForInputRead(decl, "specialization-template-request")
	product, err := g.result.SpecializeFunctionWithInputAudit(decl, args)
	if product != nil {
		reads, _ := product.InputReadObservations()
		g.inputReads.semanticObservations(reads)
	}
	return product, err
}
