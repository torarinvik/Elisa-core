//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// fnFastMath reports whether FULL fast-math FP applies to the value currently being emitted: the
// enclosing function opted in (@fast_math) or the whole program did (the `-ffast-math` CLI flag /
// ELISACORE_FAST_MATH). A comprehension fold's accumulator reassociation is a narrower tier
// (reassoc+contract only) and is handled separately via reduceReassocScope, not here.
func (s *functionState) fnFastMath() bool {
	if s == nil {
		return false
	}
	if s.fnType != nil && s.fnType.FastMath {
		return true
	}
	if s.g != nil && s.g.globalFastMath {
		return true
	}
	return false
}

// fpStrict reports whether the program opted out of relaxed FP entirely (ELISACORE_STRICT_FP /
// the `-fstrict-fp` CLI flag). It beats every other tier, @fast_math included: a program asking for
// reproducible IEEE results is asking for them everywhere, and a single contracted multiply-add in
// a hot helper is exactly the kind of thing that would otherwise slip through.
func (s *functionState) fpStrict() bool {
	return s != nil && s.g != nil && s.g.strictFP
}

// Default float tier: clang's -ffp-contract=on. A multiply may fuse into an add or subtract only
// when the add consumes the product directly inside one source expression (`a * b + c`). Products
// that went through a local, a call or another statement are never contracted, so splitting an
// expression across statements keeps two roundings, as it does in C. Division allows a reciprocal
// (`arcp`) but not contraction. @fast_math / -ffast-math and fold accumulators keep their wider tiers.

// fpRelaxedTier applies the strict, fast-math or fold-reassociation tier to v and reports whether
// one of them decided the flags; false means the default tier applies.
func (s *functionState) fpRelaxedTier(v llvmIRValueRef) bool {
	switch {
	case s.fpStrict():
		return true
	case s.fnFastMath():
		llvmSetFPFast(v)
		return true
	case s.reduceReassocScope > 0:
		llvmSetFPReassoc(v)
		return true
	}
	return false
}

// fpMultiply flags an fmul. In the default tier it carries no flags until an enclosing add or
// subtract in the same expression marks it contractable (fpAddSub).
func (s *functionState) fpMultiply(v llvmIRValueRef) llvmIRValueRef {
	s.fpRelaxedTier(v)
	return v
}

// fpAddSub flags an fadd/fsub. In the default tier, an operand that is a direct float product in the
// same expression (operandExpressions[i] is `x * y`, parentheses aside, and lowered to an fmul) is
// marked contractable together with the add, which lets the backend form one fused multiply-add.
func (s *functionState) fpAddSub(v llvmIRValueRef, operands [2]llvmIRValueRef, operandExpressions [2]ast.Expr) llvmIRValueRef {
	if s.fpRelaxedTier(v) {
		return v
	}
	contracted := false
	for index, operand := range operands {
		if isDirectProductExpression(operandExpressions[index]) && llvmIsFMul(operand) {
			llvmSetFPContract(operand)
			contracted = true
		}
	}
	if contracted {
		llvmSetFPContract(v)
	}
	return v
}

// fpDivide flags an fdiv: reciprocal formation only in the default tier.
func (s *functionState) fpDivide(v llvmIRValueRef) llvmIRValueRef {
	if !s.fpRelaxedTier(v) {
		llvmSetFPReciprocal(v)
	}
	return v
}

// fpAugmented flags a compound float assignment like the binary operator it spells: `total += a * b`
// is one expression, so its product may fuse into the add (clang contracts it too).
func (s *functionState) fpAugmented(op lexer.TokenKind, result, current, value llvmIRValueRef, valueExpression ast.Expr) {
	switch op {
	case lexer.TOKEN_PLUSEQ, lexer.TOKEN_MINUSEQ:
		s.fpAddSub(result, [2]llvmIRValueRef{current, value}, [2]ast.Expr{nil, valueExpression})
	case lexer.TOKEN_STAREQ:
		s.fpMultiply(result)
	case lexer.TOKEN_SLASHEQ:
		s.fpDivide(result)
	}
}

func isDirectProductExpression(expression ast.Expr) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.Inner
	}
	binary, ok := expression.(*ast.BinaryExpr)
	return ok && binary.Op == lexer.TOKEN_STAR && binary.LoweredCall == nil
}
