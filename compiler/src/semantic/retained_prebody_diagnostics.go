package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func (r *RetainedAnalysis) retainedShiftedPreBodyDiagnostics(pairedNew map[*ast.FuncDecl]*ast.FuncDecl, marks map[string][][2]int) []Diagnostic {
	diags := append([]Diagnostic(nil), r.pre.diags...)
	owners := retainedDiagnosticOwners(r.decls, nil, func(decl ast.Decl) ast.Decl {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			return pairedNew[fn]
		}
		return nil
	})
	for i := range diags {
		if strings.Contains(diags[i].Message, "__") {
			if owner, shift := owners.ownerAt(diags[i].Pos); owner != nil {
				diags[i].Message = retainedShiftLocalGensyms(diags[i].Message, shift)
			}
		}
		diags[i].Pos = retainedShiftPos(diags[i].Pos, marks)
	}
	return diags
}
