package semantic

import (
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// P4 (docs/87 + effect/error variance): protocol-method conformance for the EFFECT, ERROR, and
// FRAME (`changes`) channels. P1 owns requires/ensure value-contract variance; this file owns the
// three capability/error/frame channels and is kept deliberately separate so the two efforts merge
// trivially. The rule on every channel is SUBSET variance: the impl method may do LESS than the
// protocol promised, never more.
//
//	impl.can[..]     ⊆ protocol.can[..]      (no extra capability used)
//	impl.error[..]   ⊆ protocol.error[..]    (no extra failure mode raised)
//	impl.changes ⊆   protocol.changes        (no place written outside the declared frame)
//
// The frame subset is the safety win: a caller of `p.push(x)` through the protocol may rely that no
// place outside `protocol push`'s `changes` set is mutated, regardless of which impl runs — so a
// sibling field / captured variable is provably untouched across the abstraction boundary.

// DISPATCH COST MODEL (P4 — the performance half).
//
// Elisa protocol dispatch is purely STATIC / monomorphized: there is no `dyn`/existential/vtable
// form (see the exploration in the P4 task — no dynamic protocol type exists in the parser, AST, or
// type system). A protocol-bounded generic `def hot[D: FieldDecoder](d: D, w: D.Word)` resolves the
// concrete impl at the instantiation site (`hot[NetDecoder](...)`), so the impl — and therefore its
// (subset-checked) `requires`/`ensure` contracts — is fully known at the call site and discharged
// there at compile time by the existing requires/return refinement-discharge machinery. The release
// build pays ZERO runtime cost for the contract: a proven obligation emits no check, and an
// unproven-but-debug-checked obligation is elided at -O1+/without -fbounds-check (see
// refinement_discharge_runtime_test.go / llvm_refinement_checks.go).
//
// If/when a dynamic protocol form (`dyn FieldDecoder`) is added, the impl is unknown at the call
// site, so the protocol method's `requires` cannot be discharged statically and must degrade to a
// DEBUG-GATED runtime guard at the dynamic-dispatch boundary (present at -O0, elided at -O3) — the
// same emit path as a boundary call-arg refinement check. No such form exists today, so the static
// (zero-cost) path is the only path and is correct as-is; this comment is the contract for the
// dynamic extension.

// checkImplEffectErrorFrameVariance runs the three subset checks for one impl method against its
// protocol method. expected/actual are the (Self-specialized) protocol and impl FuncTypes; protoDecl
// is the protocol method's AST decl (for its `changes` paths), implDecl the impl method's. Any
// violation is reported on implPos with a clear, channel-specific message.
func (a *Analyzer) checkImplEffectErrorFrameVariance(
	interfaceName, methodName string,
	expected, actual *FuncType,
	protoDecl, implDecl ast.Node,
	implPos lexer.Pos,
) {
	a.checkImplEffectSubset(interfaceName, methodName, expected, actual, implPos)
	a.checkImplErrorSubset(interfaceName, methodName, expected, actual, implPos)
	a.checkImplChangesSubset(interfaceName, methodName, protoDecl, implDecl, implPos)
}

// checkImplEffectSubset enforces impl.can[..] ⊆ protocol.can[..]: the impl may not use a capability
// the protocol method did not declare. (Permission-PARAMETER polymorphism is left to the structural
// signature match; here we only compare the resolved concrete capability names.)
func (a *Analyzer) checkImplEffectSubset(interfaceName, methodName string, expected, actual *FuncType, implPos lexer.Pos) {
	if expected == nil || actual == nil {
		return
	}
	// A permission-PARAMETER on the protocol method (`can[P]`) is opaquely permissive: it binds to
	// whatever concrete capability the impl uses, so do not diagnose against a parametric grant.
	if len(expected.PermissionParams) > 0 {
		return
	}
	// Compare at MEMBER granularity (`Atomics.Load`), not family (`Atomics`): a protocol that grants
	// `Atomics.Load` must reject an impl that also wants `Atomics.Store`. A protocol family grant with
	// no member (`Atomics`) subsumes any member under it.
	allowedFamily := map[string]bool{}
	allowedMember := map[string]bool{}
	for _, ref := range expected.PermissionRefs {
		if ref.Member == "" {
			allowedFamily[ref.Name] = true
		}
		allowedMember[permissionRefKey(ref)] = true
	}
	var extra []string
	for _, ref := range actual.PermissionRefs {
		if allowedFamily[ref.Name] || allowedMember[permissionRefKey(ref)] {
			continue
		}
		extra = append(extra, permissionRefKey(ref))
	}
	if len(extra) == 0 {
		return
	}
	sort.Strings(extra)
	a.errorf(implPos, "impl method %q for interface %q uses capability %s not granted by the protocol's `can[..]` set (impl effects must be a subset of the protocol's)",
		methodName, interfaceName, quoteJoin(extra))
}

// checkImplErrorSubset enforces impl.error[..] ⊆ protocol.error[..]: the impl may not raise a failure
// mode the protocol method did not declare. An impl that raises NO errors (plain return) trivially
// conforms to any protocol error set.
func (a *Analyzer) checkImplErrorSubset(interfaceName, methodName string, expected, actual *FuncType, implPos lexer.Pos) {
	if expected == nil || actual == nil {
		return
	}
	protoSet := returnErrorSet(expected.Return)
	implSet := returnErrorSet(actual.Return)
	if implSet == nil {
		return // impl declares no errors: subset of anything
	}
	allowed := map[string]bool{}
	if protoSet != nil {
		for _, t := range protoSet.Tags {
			allowed[t] = true
		}
		// An error-set PARAMETER on the protocol (`error[R]`) is opaquely permissive: it binds to
		// whatever set the impl raises, so do not diagnose against a parametric protocol set.
		if protoSet.HasParams() {
			return
		}
	}
	var extra []string
	for _, t := range implSet.Tags {
		if !allowed[t] {
			extra = append(extra, t)
		}
	}
	if len(extra) == 0 {
		return
	}
	sort.Strings(extra)
	a.errorf(implPos, "impl method %q for interface %q raises error %s not declared in the protocol's `error[..]` set (impl errors must be a subset of the protocol's)",
		methodName, interfaceName, quoteJoin(extra))
}

// checkImplChangesSubset enforces impl.changes ⊆ protocol.changes: every place the impl declares it
// `changes` must lie inside some place the protocol method declared it `changes`. The protocol's
// frame is the contract a caller-through-the-protocol relies on; an impl widening it (writing a
// sibling field / an unmentioned place) breaks that reliance and is rejected.
//
// If the protocol method declares NO `changes` clause it is unframed (no promise to callers) and any
// impl frame conforms. The places are field-granular paths rooted at the receiver parameter; both
// sides root their frame at their own receiver name, so we compare by field-suffix prefix-cover and
// ignore the root spelling.
func (a *Analyzer) checkImplChangesSubset(interfaceName, methodName string, protoDecl, implDecl ast.Node, implPos lexer.Pos) {
	protoChanges := declChangesPaths(protoDecl)
	if len(protoChanges) == 0 {
		return // unframed protocol method: any impl frame conforms
	}
	implChanges := declChangesPaths(implDecl)
	if len(implChanges) == 0 {
		return // impl writes nothing it declares: trivially a subset
	}
	// Field-suffix prefix-cover: an impl path `self.a.b` is covered iff some protocol path shares a
	// receiver-rooted prefix (protocol `self.a` covers `self.a` and `self.a.b`; bare `self` covers all).
	covers := func(implFields []string) bool {
		for _, pp := range protoChanges {
			if len(pp.Fields) > len(implFields) {
				continue
			}
			match := true
			for i, f := range pp.Fields {
				if implFields[i] != f {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
		return false
	}
	for _, ip := range implChanges {
		if !covers(ip.Fields) {
			a.errorf(implPos, "impl method %q for interface %q writes %q outside the protocol's `changes` frame (impl frame must be a subset of the protocol's)",
				methodName, interfaceName, frameWriteString("self", ip.Fields))
		}
	}
}

// declChangesPaths returns the `changes` paths declared on a method's AST decl, normalized so each
// path is rooted at the method's receiver (the leading parameter). Only the field suffix is retained
// because the protocol and impl spell their receiver independently (both conventionally `self`).
func declChangesPaths(decl ast.Node) []ast.EnsuresPath {
	switch n := decl.(type) {
	case *ast.FuncDecl:
		if n == nil {
			return nil
		}
		return receiverRootedChanges(n.Changes, n.Params)
	case *ast.ExternFuncDecl:
		if n == nil {
			return nil
		}
		return receiverRootedChanges(n.Changes, n.Params)
	default:
		return nil
	}
}

// receiverRootedChanges keeps only `changes` paths whose root is the receiver parameter (index 0),
// so cross-side comparison is by field suffix alone.
func receiverRootedChanges(paths []ast.EnsuresPath, params []ast.ParamDecl) []ast.EnsuresPath {
	if len(paths) == 0 || len(params) == 0 {
		return nil
	}
	recv := params[0].Name
	var out []ast.EnsuresPath
	for _, p := range paths {
		if p.Root == recv {
			out = append(out, p)
		}
	}
	return out
}

// returnErrorSet extracts the declared error set from a return type, or nil if the return is not an
// error union.
func returnErrorSet(ret Type) *ErrorSetType {
	if u, ok := ret.(*ErrorUnionType); ok {
		return u.Errors
	}
	return nil
}

// stripEffectChannels returns a shallow copy of a function signature with the effect and error
// channels neutralized: the resolved capability list is cleared and an error-union return is reduced
// to its underlying value type. The structural conformance match (SameType) compares these copies so
// that effect/error SUBSET differences are tolerated there and diagnosed by the dedicated variance
// checks instead of being rejected as a signature mismatch. Permission/error-set PARAMETERS are left
// intact so genuine polymorphism still participates in the structural match.
func stripEffectChannels(ft *FuncType) *FuncType {
	if ft == nil {
		return nil
	}
	clone := *ft
	clone.Permissions = nil
	clone.PermissionRefs = nil
	clone.DeclaredPermissions = nil
	clone.DeclaredPermissionRefs = nil
	if u, ok := clone.Return.(*ErrorUnionType); ok && u != nil {
		clone.Return = u.Value
	}
	return &clone
}

func quoteJoin(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = "`" + s + "`"
	}
	return strings.Join(parts, ", ")
}
