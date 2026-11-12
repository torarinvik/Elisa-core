package semantic

import "elisacore/src/lexer"

// joinRuleSite is one candidate branch statement: its position and the outer variables its
// fall-through arms phi-reconstruct.
type joinRuleSite struct {
	pos  lexer.Pos
	vars []string
}
