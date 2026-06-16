//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

// elisa_coreDisjointMDString / Node mirror the helpers in
// llvm_exprs_llvmvalueiszeroconstant_*.go, duplicated here because cgo C
// definitions are per-file. They build the alias-scope metadata graph used to
// stamp disjoint-parameter element accesses (docs/84 §3.3 Increment 3b).
static LLVMMetadataRef elisa_coreDisjointMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreDisjointDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreDisjointMDString(ctx, domainName);
	return LLVMMDNodeInContext2(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreDisjointScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreDisjointMDString(ctx, scopeName);
	operands[1] = domain;
	return LLVMMDNodeInContext2(ctx, operands, 2);
}

static void elisa_coreDisjointSetMD(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = LLVMMDNodeInContext2(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName)), listValue);
}

// elisa_coreAttachDisjointParamScope stamps one element load/store with its own
// alias.scope (aliasScopeName) and a noalias list of N sibling scope names, all in
// a single domain (domainName). One domain per disjoint group is mandatory for
// LLVM's LoopAccessAnalysis memcheck elision (docs/84 §3.3 caveat b).
//
// Fail-closed: if either the own scope is empty/NULL or the sibling list is empty,
// NOTHING is stamped. A tagged access must always carry BOTH lists in the same
// domain; emitting alias.scope without the sibling noalias keeps all guards
// silently (caveat a), so we refuse to emit a half-tag.
static void elisa_coreAttachDisjointParamScope(LLVMValueRef inst, LLVMContextRef ctx,
	const char* domainName, const char* aliasScopeName,
	const char** noAliasScopeNames, size_t noAliasCount) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	if (aliasScopeName[0] == '\0' || noAliasCount == 0 || noAliasScopeNames == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreDisjointDomain(ctx, domainName);

	LLVMMetadataRef ownScope[1];
	ownScope[0] = elisa_coreDisjointScope(ctx, domain, aliasScopeName);
	elisa_coreDisjointSetMD(inst, ctx, "alias.scope", ownScope, 1);

	LLVMMetadataRef* siblings = (LLVMMetadataRef*)malloc(sizeof(LLVMMetadataRef) * noAliasCount);
	if (siblings == NULL) {
		return;
	}
	size_t n = 0;
	for (size_t i = 0; i < noAliasCount; i++) {
		if (noAliasScopeNames[i] == NULL || noAliasScopeNames[i][0] == '\0') {
			continue;
		}
		siblings[n++] = elisa_coreDisjointScope(ctx, domain, noAliasScopeNames[i]);
	}
	if (n != 0) {
		elisa_coreDisjointSetMD(inst, ctx, "noalias", siblings, n);
	}
	free(siblings);
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

// disjointParamScope holds the alias-scope identity assigned to one proven-distinct
// container-ref parameter for the duration of a function body (docs/84 Increment 3b).
type disjointParamScope struct {
	// alloca is the LLVM binding pointer of the parameter (its entry alloca, or the
	// direct pointer for memory-class params). It is the shadowing-proof identity key:
	// a local that shadows the param name rebinds to a DIFFERENT pointer, so the
	// equality check at the index site fails and we fall back to the shared elt scope.
	alloca C.LLVMValueRef
	// ownScope is this param's distinct element scope name (e.g. "elt.p0").
	ownScope string
	// siblingScopes are the scope names of the OTHER container params this one is
	// proven distinct from. The element access is stamped !noalias = these.
	siblingScopes []string
}

// disjointParamScopeState is the per-function disjoint-parameter tagging context. It is
// nil unless `-fnoalias` is on AND the analyzer proved a self-noalias group for this fn.
type disjointParamScopeState struct {
	domain   string
	byParam  map[int]*disjointParamScope
	byAlloca map[C.LLVMValueRef]*disjointParamScope
}

// initDisjointParamScopes computes, once per function body, the per-parameter alias
// scopes derived from the whole-program FuncDisjointParams fact. It is the consumer of
// Increment 3a. Guarded entirely behind -fnoalias (g.noaliasMutableRefs): with the flag
// off it is a no-op and the existing shared hdr/elt tagging is untouched.
//
// Only params in the SelfNoalias set get a scope: such a param is proven distinct from
// EVERY other container param at every call site, so its sibling-noalias list is the
// (complete) set of the other group members and both metadata lists are guaranteed
// non-empty and in the same domain — the both-lists-same-domain assertion of §3.3 holds
// by construction. Pairwise-only distinctness (distinct from some but not all siblings)
// is intentionally NOT tagged: it cannot, on its own, license the memcheck elision and
// risks an asymmetric tag, so we fail closed to the shared elt scope.
func (s *functionState) initDisjointParamScopes() {
	if s == nil || s.g == nil || !s.g.noaliasMutableRefs {
		return
	}
	if s.decl == nil || s.g.result == nil || s.g.result.FuncDisjointParams == nil {
		return
	}
	info := s.g.result.FuncDisjointParams[s.decl]
	if info == nil || len(info.SelfNoalias) < 2 {
		// Need at least two mutually-distinct params for a non-empty sibling list.
		return
	}

	// The self-noalias group: every member is proven distinct from every other container
	// param, hence pairwise distinct from every OTHER group member.
	group := make([]int, 0, len(info.SelfNoalias))
	for idx, ok := range info.SelfNoalias {
		if ok {
			group = append(group, idx)
		}
	}
	if len(group) < 2 {
		return
	}

	st := &disjointParamScopeState{
		domain:   fmt.Sprintf("elisa.disjoint.%s.aa", sanitizeIdentifier(s.decl.Name)),
		byParam:  map[int]*disjointParamScope{},
		byAlloca: map[C.LLVMValueRef]*disjointParamScope{},
	}
	for _, idx := range group {
		// Only a container-ref param to a numeric-element darray is eligible: the inner
		// buffer must be scalar so element memory provably never aliases header memory
		// (mirrors the isNumericType gate on the shared hdr/elt tagging). A non-numeric
		// element (nested darray) keeps the shared elt scope.
		if !s.disjointParamHasNumericElems(idx) {
			continue
		}
		siblings := make([]string, 0, len(group)-1)
		ok := true
		for _, other := range group {
			if other == idx {
				continue
			}
			// Defensive: confirm the pair really is recorded distinct. For a SelfNoalias
			// group this is always true, but check anyway so a future analyzer change can
			// only lose optimization, never produce an asymmetric (unsound) tag.
			if !info.PairDistinct(idx, other) {
				ok = false
				break
			}
			siblings = append(siblings, disjointScopeName(other))
		}
		if !ok || len(siblings) == 0 {
			continue
		}
		st.byParam[idx] = &disjointParamScope{
			ownScope:      disjointScopeName(idx),
			siblingScopes: siblings,
		}
	}
	if len(st.byParam) < 2 {
		// A single scope has no sibling to be noalias from; drop the whole group.
		return
	}
	s.disjointScopes = st
}

func disjointScopeName(paramIndex int) string {
	return fmt.Sprintf("elt.p%d", paramIndex)
}

// disjointParamHasNumericElems reports whether parameter `paramIndex` is a
// reference to a darray whose element type is a scalar numeric type.
func (s *functionState) disjointParamHasNumericElems(paramIndex int) bool {
	if s == nil || s.fnType == nil || paramIndex < 0 || paramIndex >= len(s.fnType.Params) {
		return false
	}
	ref, ok := s.fnType.Params[paramIndex].(*semantic.RefType)
	if !ok || ref == nil {
		return false
	}
	darr, ok := semantic.StripAggregateStateType(ref.Elem).(*semantic.DArrayType)
	if !ok || darr == nil {
		return false
	}
	return isNumericType(darr.Elem)
}

// recordDisjointParamAlloca binds a parameter's LLVM alloca (its shadowing-proof
// identity) to its assigned scope, after the param has been bound at body setup.
func (s *functionState) recordDisjointParamAlloca(paramIndex int, alloca C.LLVMValueRef) {
	if s == nil || s.disjointScopes == nil || alloca == nil {
		return
	}
	scope, ok := s.disjointScopes.byParam[paramIndex]
	if !ok {
		return
	}
	scope.alloca = alloca
	s.disjointScopes.byAlloca[alloca] = scope
}

// disjointScopeForObject resolves the index-site object expression back to a
// proven-distinct parameter and returns its scope. The mapping is SOUND against
// name shadowing: it resolves the object's current binding pointer and requires it to
// equal the parameter's recorded alloca. A shadowing local rebinds the name to a
// different pointer, so the lookup misses and the caller falls back to the shared scope.
// Returns nil when the object is not a proven-distinct parameter.
func (s *functionState) disjointScopeForObject(objExpr ast.Expr) *disjointParamScope {
	if s == nil || s.disjointScopes == nil || objExpr == nil {
		return nil
	}
	ident, ok := stripBackendParens(objExpr).(*ast.Ident)
	if !ok || ident == nil {
		return nil
	}
	binding, ok := s.lookupBinding(ident.Name)
	if !ok || binding.ptr == nil {
		return nil
	}
	return s.disjointScopes.byAlloca[binding.ptr]
}

// stripBackendParens peels ParenExpr wrappers so an object like `(y)[i]` still resolves.
func stripBackendParens(e ast.Expr) ast.Expr {
	for {
		paren, ok := e.(*ast.ParenExpr)
		if !ok || paren == nil {
			return e
		}
		e = paren.Inner
	}
}

// tagDisjointParamElementAccess stamps an element load/store with the per-parameter
// alias.scope + sibling noalias metadata (docs/84 §3.3). It REPLACES the shared elt
// scope for proven-distinct params: the access is in a distinct per-function domain, so
// LLVM's LoopAccessAnalysis can mark the cross-param dependence NoAlias and elide the
// runtime memcheck. The both-lists-same-domain invariant is enforced in the C helper
// (it refuses to emit alias.scope without a non-empty sibling noalias list).
func (s *functionState) tagDisjointParamElementAccess(inst C.LLVMValueRef, scope *disjointParamScope) {
	if s == nil || s.g == nil || inst == nil || scope == nil || s.disjointScopes == nil {
		return
	}
	if scope.ownScope == "" || len(scope.siblingScopes) == 0 {
		return
	}
	if C.LLVMIsALoadInst(inst) == nil && C.LLVMIsAStoreInst(inst) == nil {
		return
	}
	domainC := cString(s.disjointScopes.domain)
	defer C.free(unsafe.Pointer(domainC))
	ownC := cString(scope.ownScope)
	defer C.free(unsafe.Pointer(ownC))

	cStrs := make([]*C.char, len(scope.siblingScopes))
	for i, name := range scope.siblingScopes {
		cStrs[i] = cString(name)
	}
	defer func() {
		for _, p := range cStrs {
			C.free(unsafe.Pointer(p))
		}
	}()
	siblingPtr := (**C.char)(unsafe.Pointer(&cStrs[0]))
	C.elisa_coreAttachDisjointParamScope(inst, s.g.context, domainC, ownC, siblingPtr, C.size_t(len(cStrs)))
}
