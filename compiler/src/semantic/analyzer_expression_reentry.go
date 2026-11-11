package semantic

import "elisacore/src/ast"

// Type resolution and proof setup sometimes need the ordinary expression
// checker. Keep those re-entry calls indirect so Go can process these parts
// of the analyzer in separate escape-analysis batches.
var analyzeExprForTypeOrProof func(*Analyzer, ast.Expr) Type

func init() {
	analyzeExprForTypeOrProof = (*Analyzer).analyzeExpr
}
