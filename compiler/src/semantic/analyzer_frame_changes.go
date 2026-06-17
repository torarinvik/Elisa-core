package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// framePath is a resolved `changes` target (docs/87): a ref parameter root plus a field chain.
// Index steps are dropped at resolve/extract time, so the path is field-granular (`r.arr` covers
// every `r.arr[i]`).
type framePath struct {
	root   string
	fields []string
}

// resolveFramePaths validates a function's frame clause (`changes` or `preserves`) and returns the
// resolved paths. Each path must root at a parameter that is a reference (writes only reach the
// caller through a ref); a non-ref or unknown root is an error. clause names the clause for errors.
func (a *Analyzer) resolveFramePaths(paths []ast.EnsuresPath, clause string) []framePath {
	if len(paths) == 0 {
		return nil
	}
	out := make([]framePath, 0, len(paths))
	for _, p := range paths {
		sym, ok := a.currentScope.Lookup(p.Root)
		if !ok || sym == nil || sym.Kind != SymbolParam {
			a.errorf(p.Position, "`%s` target %q is not a parameter", clause, p.Root)
			continue
		}
		if !isFrameWritableRefType(sym.Type) {
			a.errorf(p.Position, "`%s %s` has no effect: %q is not a mutable reference parameter (only writes through a ref reach the caller)", clause, frameTargetString(p), p.Root)
			continue
		}
		out = append(out, framePath{root: p.Root, fields: append([]string(nil), p.Fields...)})
	}
	return out
}

// resolveFrameSummary computes a callee's EFFECTIVE write frame (docs/87 87-3) for its FuncType: the
// (param index, field suffix) places it may write through its reference parameters, drawn from its
// direct `changes` paths AND its `fulfills <param> is <FrameLaw>` clauses (the law's own `changes`
// paths, rebound from the law subject to the named param). frameBounded reports whether the callee
// has ANY such write-bounding clause — only then may a caller refine a mutable-ref argument. This
// runs at signature-collection time (laws are already lookable, as resolveRefinementEnsures relies
// on); no body/type info is needed, only param names and law shapes. It does NOT diagnose — the
// authoritative validation runs in the callee's own analyzeFunc (resolveFramePaths / expandFulfills);
// here a malformed clause is simply skipped so a summary never over-claims a bound.
func (a *Analyzer) resolveFrameSummary(params []ast.ParamDecl, changes []ast.EnsuresPath, fulfills []ast.FulfillsClause) ([]FrameParamWrite, bool) {
	indexOf := func(name string) int {
		for i, p := range params {
			if p.Name == name {
				return i
			}
		}
		return -1
	}
	var out []FrameParamWrite
	bounded := false
	for _, p := range changes {
		idx := indexOf(p.Root)
		if idx < 0 {
			continue
		}
		bounded = true
		out = append(out, FrameParamWrite{ParamIndex: idx, Fields: append([]string(nil), p.Fields...)})
	}
	for _, fc := range fulfills {
		idx := indexOf(fc.Param)
		if idx < 0 {
			continue
		}
		decl, _, ok := a.lookupLaw(fc.Law)
		if !ok || decl == nil || !isFrameLaw(decl) || len(decl.Params) == 0 {
			continue
		}
		subject := decl.Params[0].Name
		for _, lp := range decl.Changes {
			if lp.Root != subject {
				continue
			}
			bounded = true
			out = append(out, FrameParamWrite{ParamIndex: idx, Fields: append([]string(nil), lp.Fields...)})
		}
	}
	return out, bounded
}

// cloneFrameWrites deep-copies a frame-write summary (each entry's Fields slice is owned).
func cloneFrameWrites(in []FrameParamWrite) []FrameParamWrite {
	if len(in) == 0 {
		return nil
	}
	out := make([]FrameParamWrite, len(in))
	for i, w := range in {
		out[i] = FrameParamWrite{ParamIndex: w.ParamIndex, Fields: append([]string(nil), w.Fields...)}
	}
	return out
}

// checkFrameConsistency enforces the docs/87 §7 identity `preserves Y ⟺ Y ∩ changes(f) = ∅`: a
// place may not be in both clauses. An overlap is contradictory (it would be both required-writable
// and forbidden-to-write).
func (a *Analyzer) checkFrameConsistency(fn *ast.FuncDecl) {
	for _, pres := range a.currentPreservesPaths {
		for _, chg := range a.currentChangesPaths {
			if framePathsOverlap(pres, chg) {
				a.errorf(fn.Pos(), "`preserves %s` conflicts with `changes %s`: a place cannot be both preserved and changed", frameWriteString(pres.root, pres.fields), frameWriteString(chg.root, chg.fields))
			}
		}
	}
}

// isFrameLaw reports whether a law declaration is a FRAME law (docs/88): a named `changes`/
// `preserves` set with no predicate body, vs a value law (`= <bool-expr>`).
func isFrameLaw(decl *ast.FuncDecl) bool {
	return decl != nil && decl.IsLaw && (len(decl.Changes) != 0 || len(decl.Preserves) != 0)
}

// expandFulfills applies each `fulfills <param> is <FrameLaw>` clause (docs/88) by rebinding the
// law's frame paths from its subject (`self`) to the named param and appending them to the
// function's resolved `changes` / `preserves` sets — so enforcement is exactly docs/87, no new
// checker. Diagnoses a missing/non-frame law, an unknown or non-ref param, and subject mismatch.
func (a *Analyzer) expandFulfills(fn *ast.FuncDecl) {
	for _, fc := range fn.Fulfills {
		if isBuiltinFunctionLevelLaw(fc.Law) {
			continue // built-in shape/measure law (docs/89); discharged elsewhere (checkFunctionLevelFulfills / backend verifier)
		}
		decl, _, ok := a.lookupLaw(fc.Law)
		if !ok || decl == nil {
			a.errorf(fc.Position, "`fulfills` names %q, which is not a law", fc.Law)
			continue
		}
		if isEffectLaw(decl) || isCompositeLaw(decl) {
			continue // function-level laws are discharged after the body is analyzed (checkFunctionLevelFulfills)
		}
		if !isFrameLaw(decl) {
			a.errorf(fc.Position, "`fulfills` requires a frame law (a `changes`/`preserves` law); %q is a value law — use it with `is` in a contract instead", fc.Law)
			continue
		}
		if fc.Param == "" {
			a.errorf(fc.Position, "frame law %q needs a subject: write `fulfills <param> is %s`", fc.Law, fc.Law)
			continue
		}
		sym, found := a.currentScope.Lookup(fc.Param)
		if !found || sym == nil || sym.Kind != SymbolParam {
			a.errorf(fc.Position, "`fulfills %s is %s`: %q is not a parameter", fc.Param, fc.Law, fc.Param)
			continue
		}
		if !isFrameWritableRefType(sym.Type) {
			a.errorf(fc.Position, "`fulfills %s is %s`: %q is not a reference parameter, so it exposes no caller-visible state to frame", fc.Param, fc.Law, fc.Param)
			continue
		}
		subject := decl.Params[0].Name
		for _, p := range decl.Changes {
			if p.Root != subject {
				continue
			}
			a.currentChangesPaths = append(a.currentChangesPaths, framePath{root: fc.Param, fields: append([]string(nil), p.Fields...)})
			a.currentHasChanges = true
		}
		for _, p := range decl.Preserves {
			if p.Root != subject {
				continue
			}
			a.currentPreservesPaths = append(a.currentPreservesPaths, framePath{root: fc.Param, fields: append([]string(nil), p.Fields...)})
			a.currentHasPreserves = true
		}
	}
}

// isEffectLaw reports whether a law declaration is an EFFECT law (docs/85 §4, Stage 4): a named set
// of forbidden effects, discharged against a function's inferred effect set, applied with the
// subject-free `fulfills <Law>`.
func isEffectLaw(decl *ast.FuncDecl) bool {
	return decl != nil && decl.IsLaw && len(decl.Forbids) != 0
}

// isCompositeLaw reports whether a law declaration is a COMPOSITE law (docs/85 §6): a named union of
// member function-level laws, applied with the subject-free `fulfills <Law>`.
func isCompositeLaw(decl *ast.FuncDecl) bool {
	return decl != nil && decl.IsLaw && len(decl.Includes) != 0
}

// isBuiltinMeasureLaw reports whether a name is a built-in MEASURE law (docs/85 §8, Stage 5): an
// emergent, codegen-dependent property verified post-hoc (never proved, never build-gating, never a
// composable premise — the §8 hard rule). Like shape laws these are predefined names, not user `law`
// decls. Only `Vectorizes` exists today.
func isBuiltinMeasureLaw(name string) bool {
	switch name {
	case "Vectorizes":
		return true
	default:
		return false
	}
}

// isBuiltinFunctionLevelLaw reports whether a name is any built-in (non-decl) function-level law — a
// shape or measure property. Such names have no FuncDecl, so `fulfills`/`is` handling must recognize
// them before falling through to law lookup.
func isBuiltinFunctionLevelLaw(name string) bool {
	return isBuiltinShapeLaw(name) || isBuiltinMeasureLaw(name)
}

// fulfillsVectorizes reports whether a function declares `fulfills Vectorizes` (subject-free). Measure
// laws are not composable, so only a direct, well-formed clause counts.
func fulfillsVectorizes(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	for _, fc := range fn.Fulfills {
		if fc.Param == "" && fc.Law == "Vectorizes" {
			return true
		}
	}
	return false
}

// lawClassName names a law's discharge class for diagnostics (docs/85 §4).
func lawClassName(decl *ast.FuncDecl) string {
	switch {
	case isFrameLaw(decl):
		return "frame"
	case isEffectLaw(decl):
		return "effect"
	case isCompositeLaw(decl):
		return "composite"
	default:
		return "value"
	}
}

// compositeLawHasCycle reports whether following `includes` from name eventually returns to a name
// already on the current path (a transitive self-inclusion). `onPath` is the active DFS stack.
func (a *Analyzer) compositeLawHasCycle(name string, onPath map[string]bool) bool {
	if onPath[name] {
		return true
	}
	decl, _, ok := a.lookupLaw(name)
	if !ok || decl == nil || !isCompositeLaw(decl) {
		return false
	}
	onPath[name] = true
	for _, m := range decl.Includes {
		if a.compositeLawHasCycle(m, onPath) {
			return true
		}
	}
	delete(onPath, name)
	return false
}

// lawEffectiveForbids resolves the full forbidden-effect set a law contributes (docs/85 §6): its own
// `forbids` plus, transitively, every included law's forbids. `visited` breaks include cycles (a
// malformed cycle is diagnosed at the decl; here it just terminates).
func (a *Analyzer) lawEffectiveForbids(name string, visited map[string]bool) []ast.PermissionRef {
	if visited[name] {
		return nil
	}
	visited[name] = true
	decl, _, ok := a.lookupLaw(name)
	if !ok || decl == nil {
		return nil
	}
	out := append([]ast.PermissionRef(nil), decl.Forbids...)
	for _, m := range decl.Includes {
		out = append(out, a.lawEffectiveForbids(m, visited)...)
	}
	return out
}

// lawEffectiveShapes resolves the full set of built-in shape requirements a law contributes (docs/85
// §6): a built-in shape name contributes itself; a composite contributes its members' shapes,
// transitively. `visited` breaks include cycles.
func (a *Analyzer) lawEffectiveShapes(name string, visited map[string]bool) []string {
	if isBuiltinShapeLaw(name) {
		return []string{name}
	}
	if visited[name] {
		return nil
	}
	visited[name] = true
	decl, _, ok := a.lookupLaw(name)
	if !ok || decl == nil {
		return nil
	}
	var out []string
	for _, m := range decl.Includes {
		out = append(out, a.lawEffectiveShapes(m, visited)...)
	}
	return out
}

// checkFunctionLevelFulfills discharges every subject-free `fulfills <Law>` clause (docs/85 §4/§6/§8)
// after the function body is analyzed (so both fnType.PermissionRefs and currentFunctionGuardedIndexes
// are final). It handles effect, shape, and composite laws uniformly via their effective obligation:
//   - effect forbid-set (own + transitively included) ∩ the function's effect set must be empty;
//   - each required built-in shape (own + transitively included) must hold.
// Frame/value `fulfills` are handled in expandFulfills; an unknown law was already diagnosed there.
// SOUNDNESS: PermissionRefs is the transitive effect set the whole effect system trusts, and the
// shape audit counts every access that would emit a check — both over-report at worst (safe FP).
func (a *Analyzer) checkFunctionLevelFulfills(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil {
		return
	}
	for _, fc := range fn.Fulfills {
		forbids := a.lawEffectiveForbids(fc.Law, map[string]bool{})
		shapes := a.lawEffectiveShapes(fc.Law, map[string]bool{})
		measure := isBuiltinMeasureLaw(fc.Law)
		if len(forbids) == 0 && len(shapes) == 0 && !measure {
			continue // not a function-level law (frame/value/unknown handled elsewhere)
		}
		if fc.Param != "" {
			a.errorf(fc.Position, "%q constrains the whole function; write `fulfills %s`, not `fulfills %s is %s`", fc.Law, fc.Law, fc.Param, fc.Law)
			continue
		}
		// A measure law (docs/85 §8) is verify-only: there is no static obligation to discharge here.
		// `fulfills Vectorizes` opts the function's loops into the post-codegen autovec verifier (set
		// in analyzeForStmt); a failure is a -Wperf warning, never a compile error. Record it as
		// measured (the honest, weaker outcome) for the --explain report.
		if measure {
			a.recordProof(fc.Position, fn.Name, fc.Law, ProofMeasured)
			continue
		}
		violated := false
		for _, forbidden := range forbids {
			for _, used := range fnType.PermissionRefs {
				if permissionRefForbidden(used, forbidden) {
					a.errorf(fn.Pos(), "function %q `fulfills %s` but uses the `%s` effect, which %s forbids", fn.Name, fc.Law, lawEffectName(used), fc.Law)
					violated = true
					break
				}
			}
		}
		for _, shape := range shapes {
			if a.dischargeShapeRequirement(fn, fc.Law, shape) {
				violated = true
			}
		}
		// docs/85 observability: record the discharge for --explain. A clean function-level law is
		// proven by analysis (effect/shape/composite); a violation (already an error) is refuted.
		if violated {
			a.recordProof(fc.Position, fn.Name, fc.Law, ProofRefuted)
		} else {
			a.recordProof(fc.Position, fn.Name, fc.Law, ProofProvenContract)
		}
	}
}

// dischargeShapeRequirement runs the audit for one built-in shape requirement on the current
// function and reports whether it was VIOLATED. via names the law the requirement came from (the
// fulfilled law, possibly a composite).
func (a *Analyzer) dischargeShapeRequirement(fn *ast.FuncDecl, via, shape string) bool {
	switch shape {
	case "NoBoundsChecks":
		if len(a.currentFunctionGuardedIndexes) > 0 {
			a.errorf(a.currentFunctionGuardedIndexes[0], "function %q `fulfills %s` (requires NoBoundsChecks) but has an index access that is not statically proven in-bounds, so a runtime bounds check would be emitted; prove the index (e.g. iterate, or guard with a bound the prover understands)", fn.Name, via)
			return true
		}
	}
	return false
}

// isBuiltinShapeLaw reports whether a name is a built-in SHAPE law (docs/89, docs/85 §8): a local,
// statically-decidable codegen property a function can `fulfills`. Shape laws are not user `law`
// decls — they are predefined names discharged by a dedicated codegen analysis. Only `NoBoundsChecks`
// exists today (user-defined shape laws need the `includes` algebra, deferred).
func isBuiltinShapeLaw(name string) bool {
	switch name {
	case "NoBoundsChecks":
		return true
	default:
		return false
	}
}

// permissionRefForbidden reports whether a used effect ref is barred by a forbid entry: a bare family
// forbid (`Memory`) bars every member of that family; a member forbid (`Memory.Allocate`) bars only
// that exact effect.
func permissionRefForbidden(used, forbid ast.PermissionRef) bool {
	if used.Name != forbid.Name {
		return false
	}
	return forbid.Member == "" || forbid.Member == used.Member
}

// isFrameWritableRefType reports whether a param type is a reference whose pointee a callee body
// can write back to the caller (a plain or mutable ref; the write itself is gated elsewhere by
// mutability, but for frame purposes any ref param exposes caller-visible state).
func isFrameWritableRefType(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}

// checkFrameWrite enforces the active frame clauses at a direct write site: if `target`'s root is a
// ref parameter, the written place must be inside the `changes` set (if any) AND must not overlap a
// `preserves` place (docs/87 channel 1).
func (a *Analyzer) checkFrameWrite(target ast.Expr) {
	if !a.currentHasChanges && !a.currentHasPreserves {
		return
	}
	a.checkFramePlace(target, "writes to")
}

// checkFrameMutableRefArg enforces the frame at a call argument passed by MUTABLE reference: the
// callee may write the place, so it counts as a write to it (docs/87 channel 2). Immutable-borrow
// args are not writes and are not checked here.
//
// calleeSuffixes is the callee's effective frame for THIS parameter (docs/87 87-3): the field
// suffixes it may write beneath the argument place. When non-nil (the callee declared a bounding
// `changes`/`fulfills` frame), the arg is REFINED — only `arg ⊕ suffix` is treated as written, so
// `f(&r.x)` where `f changes self.a` is checked as a write to `r.x.a`, not the whole `r.x`. When nil
// (the callee's writes are unbounded), the conservative whole-place rule applies.
func (a *Analyzer) checkFrameMutableRefArg(arg ast.Expr, calleeSuffixes [][]string) {
	if !a.currentHasChanges && !a.currentHasPreserves {
		return
	}
	if calleeSuffixes == nil {
		a.checkFramePlace(arg, "may write")
		return
	}
	root, fields, ok := frameWritePath(arg)
	if !ok {
		return
	}
	for _, suffix := range calleeSuffixes {
		full := append(append([]string(nil), fields...), suffix...)
		a.checkFrameResolvedPlace(arg, root, full, "may write")
	}
}

// calleeFrameSuffixesForParam returns the field suffixes a callee may write beneath its parameter i
// (docs/87 87-3), or nil if the callee declared no bounding frame (writes unbounded → caller stays
// conservative). A callee that declares a frame but writes nothing through this param yields an
// empty (non-nil) slice, refining the arg to "no caller-visible write".
func calleeFrameSuffixesForParam(ft *FuncType, i int) [][]string {
	if ft == nil || !ft.FrameBounded {
		return nil
	}
	suffixes := [][]string{}
	for _, w := range ft.FrameWrites {
		if w.ParamIndex == i {
			suffixes = append(suffixes, w.Fields)
		}
	}
	return suffixes
}

func (a *Analyzer) checkFramePlace(place ast.Expr, verb string) {
	root, fields, ok := frameWritePath(place)
	if !ok {
		return
	}
	a.checkFrameResolvedPlace(place, root, fields, verb)
}

// checkFrameResolvedPlace enforces the active frame clauses against an already-resolved (root,
// fields) place, reporting at `place`. Shared by the direct-write path (checkFramePlace) and the
// 87-3 refined mutable-ref-arg path (which synthesizes the place by appending the callee's frame
// suffix to the argument place).
func (a *Analyzer) checkFrameResolvedPlace(place ast.Expr, root string, fields []string, verb string) {
	sym, found := a.currentScope.Lookup(root)
	if !found || sym == nil || sym.Kind != SymbolParam || !isFrameWritableRefType(sym.Type) {
		return // not a caller-visible (ref-param) place: a local write is never a frame violation
	}
	written := framePath{root: root, fields: fields}
	if a.currentHasChanges && !a.framePathCovered(root, fields) {
		a.errorf(place.Pos(), "%s %s, which is outside the `changes` set of %q", verb, frameWriteString(root, fields), a.currentFuncDecl.Name)
		return
	}
	for _, pres := range a.currentPreservesPaths {
		if framePathsOverlap(written, pres) {
			a.errorf(place.Pos(), "%s %s, which %q `preserves`", verb, frameWriteString(root, fields), a.currentFuncDecl.Name)
			return
		}
	}
}

// framePathsOverlap reports whether two frame paths touch the same storage: same root and one field
// chain is a prefix of the other (writing `r.health` touches `r.health.x`, and vice versa).
func framePathsOverlap(a, b framePath) bool {
	if a.root != b.root {
		return false
	}
	shorter := a.fields
	if len(b.fields) < len(shorter) {
		shorter = b.fields
	}
	for i := range shorter {
		if a.fields[i] != b.fields[i] {
			return false
		}
	}
	return true
}

// framePathCovered reports whether a write to (root, fields) is permitted by some declared path:
// the declared path shares the root and is a PREFIX of the written field chain (declared `r.px`
// covers `r.px` and `r.px.sub`; declared bare `r` covers everything under r).
func (a *Analyzer) framePathCovered(root string, fields []string) bool {
	for _, fp := range a.currentChangesPaths {
		if fp.root != root || len(fp.fields) > len(fields) {
			continue
		}
		match := true
		for i, f := range fp.fields {
			if fields[i] != f {
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

// frameWritePath extracts a field-granular (root, fields) place from an assignment target or a
// ref-argument expression, dropping index steps (field granularity) and address-of/paren wrappers.
func frameWritePath(expr ast.Expr) (string, []string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", nil, false
		}
		return n.Name, nil, true
	case *ast.ParenExpr:
		return frameWritePath(n.Inner)
	case *ast.AddrOfExpr:
		return frameWritePath(n.Operand)
	case *ast.FieldExpr:
		root, fields, ok := frameWritePath(n.Object)
		if !ok {
			return "", nil, false
		}
		return root, append(fields, n.Field), true
	case *ast.IndexExpr:
		// Field granularity: `r.arr[i]` is treated as a write to `r.arr`.
		return frameWritePath(n.Object)
	default:
		return "", nil, false
	}
}

func frameWriteString(root string, fields []string) string {
	if len(fields) == 0 {
		return root
	}
	return root + "." + strings.Join(fields, ".")
}

func frameTargetString(p ast.EnsuresPath) string {
	return frameWriteString(p.Root, p.Fields)
}
