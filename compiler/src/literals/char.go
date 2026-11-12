// Package literals implements source-literal decoding shared by compiler passes.
package literals

import "elisacore/src/ast"

// ParseCharLiteral accepts the one-byte character representation used by Elisa.
func ParseCharLiteral(expr *ast.CharLit) (int64, bool) {
	if expr == nil || len(expr.Value) != 1 {
		return 0, false
	}
	return int64(expr.Value[0]), true
}
