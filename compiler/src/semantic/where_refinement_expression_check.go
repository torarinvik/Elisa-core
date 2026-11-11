package semantic

import "elisacore/src/ast"

// Closed where predicates use the full expression checker. Keep this rare
// re-entry indirect so Go can analyze the type resolver and expression
// checker in separate escape-analysis batches.
var analyzeConstantWherePredicate func(*Analyzer, ast.Expr) Type

func init() {
	analyzeConstantWherePredicate = (*Analyzer).analyzeExpr
}
