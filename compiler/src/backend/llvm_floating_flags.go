//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

// Allow FP contraction (a*b+c -> fma) on a floating-point instruction, matching clang's default
// -ffp-contract=on. Only the AllowContract flag is set; no reassociation / no-nan / no-inf, so this
// does not change value semantics beyond the single-rounding fused multiply-add the C ABI permits.
static void elisacoreSetFPContract(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAllowContract);
	}
}

// Allow contraction AND reciprocal (a/b -> a * (1/b)) on a floating-point instruction, matching
// clang's -ffp-contract=fast -freciprocal-math. For a loop-invariant divisor this lets LICM hoist
// the single reciprocal out of the loop, turning a per-iteration fdiv (slow, poorly pipelined) into
// a multiply. This relaxes division rounding by up to ~1 ulp, the same relaxed-FP tier as contract.
static void elisacoreSetFPContractReciprocal(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAllowContract | LLVMFastMathAllowReciprocal);
	}
}

// Allow reassociation + contraction on an FP instruction, WITHOUT nnan/ninf/nsz. This is the
// "defined tree-order reduction" tier: it lets the vectorizer re-bracket a reduction into parallel
// partial sums (SIMD tree reduction) while leaving NaN/Inf/signed-zero behaviour at IEEE. Used on a
// comprehension fold's accumulator update — a fold's reduction order is defined as a tree, not strict
// left-to-right, so this reassociation is the defined semantics, not an unsafe opt-in (docs/79).
static void elisacoreSetFPReassoc(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAllowReassoc | LLVMFastMathAllowContract);
	}
}

// Set ALL fast-math flags (reassoc, nnan, ninf, nsz, arcp, contract) on an FP instruction, matching
// clang -ffast-math. Enables FP reassociation -> auto-vectorization of reduction/elementwise loops.
// Used only inside functions annotated @fast_math (opt-in; reorders FP, results may differ).
static void elisacoreSetFPFast(LLVMValueRef v) {
	if (v != NULL && LLVMCanValueUseFastMathFlags(v)) {
		LLVMSetFastMathFlags(v, LLVMFastMathAll);
	}
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}

// elisa_coreAttachDarrayAndComprehensionScope combines the shared darray header/element
// separation with a comprehension's fresh-source/destination separation. LLVM has one metadata
// slot for each list, so setting the two domains separately would overwrite one of them.
static void elisa_coreAttachDarrayAndComprehensionScope(LLVMValueRef inst, LLVMContextRef ctx,
	const char* comprehensionDomainName, const char* comprehensionAliasScopeName,
	const char* comprehensionNoAliasScopeName) {
	if (inst == NULL || ctx == NULL || comprehensionDomainName == NULL ||
		comprehensionAliasScopeName == NULL || comprehensionNoAliasScopeName == NULL ||
		comprehensionDomainName[0] == '\0' || comprehensionAliasScopeName[0] == '\0' ||
		comprehensionNoAliasScopeName[0] == '\0') {
		return;
	}
	LLVMMetadataRef darrayDomain = elisa_coreCreateAliasScopeDomain(ctx, "elisa.darray.aa");
	LLVMMetadataRef comprehensionDomain = elisa_coreCreateAliasScopeDomain(ctx, comprehensionDomainName);
	LLVMMetadataRef aliasScopes[2];
	aliasScopes[0] = elisa_coreCreateAliasScope(ctx, darrayDomain, "elt");
	aliasScopes[1] = elisa_coreCreateAliasScope(ctx, comprehensionDomain, comprehensionAliasScopeName);
	LLVMMetadataRef noAliasScopes[2];
	noAliasScopes[0] = elisa_coreCreateAliasScope(ctx, darrayDomain, "hdr");
	noAliasScopes[1] = elisa_coreCreateAliasScope(ctx, comprehensionDomain, comprehensionNoAliasScopeName);
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 2);
	elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, 2);
}
*/
import "C"

func fpAllowContract(v C.LLVMValueRef) C.LLVMValueRef {
	C.elisacoreSetFPContract(v)
	return v
}

// fpAllowContractReciprocal marks an FP instruction as contraction- and reciprocal-allowed, so the
// backend may hoist a loop-invariant divisor's reciprocal and replace per-iteration division with a
// multiply. Used for fdiv. Returns the same value for convenient inline use.
func fpAllowContractReciprocal(v C.LLVMValueRef) C.LLVMValueRef {
	C.elisacoreSetFPContractReciprocal(v)
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
func (s *functionState) fpContract(v C.LLVMValueRef) C.LLVMValueRef {
	if s.fpStrict() {
		return v
	}
	if s.fnFastMath() {
		C.elisacoreSetFPFast(v)
		return v
	}
	if s.reduceReassocScope > 0 {
		C.elisacoreSetFPReassoc(v)
		return v
	}
	return fpAllowContract(v)
}

// fpContractReciprocal applies contraction+reciprocal by default (for fdiv), full fast-math under
// @fast_math / -ffast-math, or reassociation+contraction inside a fold accumulator update.
func (s *functionState) fpContractReciprocal(v C.LLVMValueRef) C.LLVMValueRef {
	if s.fpStrict() {
		return v
	}
	if s.fnFastMath() {
		C.elisacoreSetFPFast(v)
		return v
	}
	if s.reduceReassocScope > 0 {
		C.elisacoreSetFPReassoc(v)
		return v
	}
	return fpAllowContractReciprocal(v)
}
