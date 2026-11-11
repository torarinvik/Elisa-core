//go:build cgo

package backend

func fpAllowContract(v llvmIRValueRef) llvmIRValueRef {
	llvmSetFPContract(v)
	return v
}

// fpAllowContractReciprocal marks an FP instruction as contraction- and reciprocal-allowed, so the
// backend may hoist a loop-invariant divisor's reciprocal and replace per-iteration division with a
// multiply. Used for fdiv. Returns the same value for convenient inline use.
func fpAllowContractReciprocal(v llvmIRValueRef) llvmIRValueRef {
	llvmSetFPContractReciprocal(v)
	return v
}

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

// fpContract applies contraction (FMA) by default, full fast-math when the function is @fast_math /
// the program is -ffast-math, or reassociation+contraction inside a comprehension fold's accumulator
// update (reduceReassocScope) so its reduction reassociates into a vectorizable tree (docs/79).
func (s *functionState) fpContract(v llvmIRValueRef) llvmIRValueRef {
	if s.fpStrict() {
		return v
	}
	if s.fnFastMath() {
		llvmSetFPFast(v)
		return v
	}
	if s.reduceReassocScope > 0 {
		llvmSetFPReassoc(v)
		return v
	}
	return fpAllowContract(v)
}

// fpContractReciprocal applies contraction+reciprocal by default (for fdiv), full fast-math under
// @fast_math / -ffast-math, or reassociation+contraction inside a fold accumulator update.
func (s *functionState) fpContractReciprocal(v llvmIRValueRef) llvmIRValueRef {
	if s.fpStrict() {
		return v
	}
	if s.fnFastMath() {
		llvmSetFPFast(v)
		return v
	}
	if s.reduceReassocScope > 0 {
		llvmSetFPReassoc(v)
		return v
	}
	return fpAllowContractReciprocal(v)
}
