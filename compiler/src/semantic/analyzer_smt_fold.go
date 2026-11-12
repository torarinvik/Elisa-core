package semantic

import (
	"elisacore/src/lexer"
	"elisacore/src/smtquery"
	"math/big"
)

// Query folding stays independently cacheable across semantic analyzer edits.
func smtIntLiteral(term string) (*big.Int, bool)               { return smtquery.IntLiteral(term) }
func smtIntPair(left, right string) (*big.Int, *big.Int, bool) { return smtquery.IntPair(left, right) }
func smtArith(op, left, right string) string                   { return smtquery.Arith(op, left, right) }
func smtModTerm(left, right string) string                     { return smtquery.ModTerm(left, right) }
func smtDivTerm(left, right string) string                     { return smtquery.DivTerm(left, right) }
func smtIteTerm(condition, yes, no string) string              { return smtquery.IteTerm(condition, yes, no) }

// smtFoldCompare evaluates a comparison of two Int literals; ok=false when either side is not ground.
func smtFoldCompare(op lexer.TokenKind, l, r string) (string, bool) {
	return smtquery.FoldCompare(op, l, r)
}
