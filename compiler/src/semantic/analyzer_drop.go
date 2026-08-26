package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/126 phase D1 — `__drop__` destructors.
//
// A type that declares `__drop__` gains RAII: the compiler calls the destructor when a
// stack value of that type dies, on EVERY exit edge of its owning scope (fall-through,
// `return`, `try` propagation). Two rules from docs/126 §1 are enforced here:
//
//  1. Declaring `__drop__` INDUCES AFFINITY. A destructor-bearing value cannot be freely
//     copied (a copy would double-close), so the compiler marks the struct affine +
//     droppable — move-only, but no must-consume obligation, because the implicit drop
//     discharges it. This is Rust's "Drop types can't be Copy", stated positively.
//  2. `__drop__` may not raise. It returns `void`, never `T error[...]`: a cleanup that
//     runs on an implicit edge must not inject an error into a scope that never called
//     it (the C++ throwing-destructor trap, closed by construction).
//
// D1 restricts drop-typed values to stack/parameter positions; region registration lists
// are D2 (docs/126 §4), synthesized aggregate/container drops are D3.

// DropHookName is the reserved destructor name (docs/126 §2, matching the `__cast__`
// runtime-dunder precedent).
const DropHookName = "__drop__"

// explicitDropMethodName is the surface spelling of an early manual release,
// `value.drop()` (docs/126 §2 "Explicit early release"). It is an ordinary consuming
// call: legal anywhere a move is, and the scope-exit drop is then statically elided.
const explicitDropMethodName = "drop"

// DropEdge names the implicit control-flow edge a compiler-inserted drop runs on. It is
// carried into diagnostics so an author can see WHICH invisible edge charged them an
// effect ("drop of `file` on `try` propagation at …", docs/126 §3).
type DropEdge string

const (
	// DropEdgeScopeExit is normal fall-through off the end of the owning scope.
	DropEdgeScopeExit DropEdge = "scope exit"
	// DropEdgeReturn is an explicit `return` that leaves the owning scope.
	DropEdgeReturn DropEdge = "`return`"
	// DropEdgeTry is a `try` propagation: the error path returns out of the scope
	// without any syntax at the drop point at all.
	DropEdgeTry DropEdge = "`try` propagation"
)

func (e DropEdge) String() string { return string(e) }

// dropHook is a registered `__drop__` destructor for one struct type.
type dropHook struct {
	// TypeName is the namespace-qualified name of the type being dropped.
	TypeName string
	// Decl is the `__drop__` declaration itself.
	Decl *ast.FuncDecl
	// Sym is the resolved function symbol (its mangled name is what codegen calls).
	Sym *Symbol
	// FnType carries the destructor's declared/inferred effects, which every scope
	// that can implicitly drop the type must be able to grant.
	FnType *FuncType
	// Struct is the type the destructor belongs to; it is marked affine + droppable.
	Struct *StructType
}

// DropSite records ONE compiler-inserted destructor call: which value dies, which type's
// `__drop__` runs, and on which implicit edge. Sites are computed by the flow analysis
// (which already knows what has been moved) and surfaced on the Result for codegen.
type DropSite struct {
	// ValueName is the source-level name of the dying value, for diagnostics.
	ValueName string
	// TypeName is the qualified name of its type.
	TypeName string
	// HookSymbol is the mangled symbol of the `__drop__` to call.
	HookSymbol string
	// Edge is the implicit edge the call sits on.
	Edge DropEdge
	// Pos is the position of the edge (the `return`, the `try`, or the scope's end).
	Pos lexer.Pos
	// DeclPos is where the dying value was declared.
	DeclPos lexer.Pos
}

// dropHookFor returns the registered destructor for t, if t is a drop type. Generic
// instances resolve through to their base struct.
func (a *Analyzer) dropHookFor(t Type) (*dropHook, bool) {
	if a == nil || len(a.dropHooks) == 0 || t == nil {
		return nil, false
	}
	base, _, _, ok := structLiteralBaseAndBindings(t)
	if !ok || base == nil {
		return nil, false
	}
	hook, ok := a.dropHooks[base.Name]
	return hook, ok
}

// isDropType reports whether t carries a `__drop__` destructor.
func (a *Analyzer) isDropType(t Type) bool {
	_, ok := a.dropHookFor(t)
	return ok
}

// collectDropHooks is the docs/126 D1 declaration pass. It runs after value symbols
// exist (so each `__drop__` has a resolved FuncType) and before body analysis (so the
// induced affinity is visible to every must-consume / move check).
func (a *Analyzer) collectDropHooks(decls []scopedDecl) {
	for _, scoped := range decls {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok || fn == nil || fn.Name != DropHookName || fn.IsContract {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			a.registerDropHook(scoped, fn)
		})
	}
}

func (a *Analyzer) registerDropHook(scoped scopedDecl, fn *ast.FuncDecl) {
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return
	}
	if len(fnType.ImplicitParamNames) != 0 {
		a.errorf(fn.Pos(), "`__drop__` must not declare implicit parameters")
		return
	}
	if funcTypeExplicitParamCount(fnType) != 1 || len(fnType.Params) != 1 {
		a.errorf(fn.Pos(), "`__drop__` must take exactly 1 parameter (the consumed receiver), got %d", funcTypeExplicitParamCount(fnType))
		return
	}
	if fnType.Variadic {
		a.errorf(fn.Pos(), "`__drop__` must not be variadic")
		return
	}
	if len(fnType.TypeParams) != 0 || len(fnType.RegionParams) != 0 {
		a.errorf(fn.Pos(), "`__drop__` must not be generic in D1; declare it on a concrete type")
		return
	}
	// No raising (docs/126 §3): the destructor runs on edges the caller never wrote, so
	// it must not be able to inject an error into them. Checked before the void check so
	// `void error[E]` gets the specific message.
	if _, isUnion := fnType.Return.(*ErrorUnionType); isUnion {
		a.errorf(fn.Pos(), "`__drop__` must not raise: it runs on implicit edges (scope exit, `return`, `try` propagation) that cannot handle an error; expose a consuming `close() -> void error[...]` for fallible teardown and keep `__drop__` as the backstop")
		return
	}
	if fnType.Return != nil && !isVoidType(fnType.Return) {
		a.errorf(fn.Pos(), "`__drop__` must return void, got %s", fnType.Return)
		return
	}
	receiver := fnType.Params[0]
	if receiver == nil || IsInvalidType(receiver) {
		return
	}
	// The receiver must be CONSUMED: taken by value, not borrowed. A `T&` receiver would
	// leave the value alive after its own destructor ran.
	if _, isRef := receiver.(*RefType); isRef {
		a.errorf(fn.Pos(), "`__drop__` must consume its receiver by value, not borrow it (`%s`)", receiver)
		return
	}
	target, _, _, ok := structLiteralBaseAndBindings(receiver)
	if !ok || target == nil {
		a.errorf(fn.Pos(), "`__drop__` receiver must be a struct type, got %s", receiver)
		return
	}
	// One per type, declared in the type's DEFINING module (docs/126 §2): a destructor is
	// part of the type's identity, so a downstream module must not bolt one on.
	if target.Namespace != scoped.Namespace {
		a.errorf(fn.Pos(), "`__drop__` for %s must be declared in that type's defining module %s, not %s",
			target.Name, moduleDisplayName(target.Namespace), moduleDisplayName(scoped.Namespace))
		return
	}
	if existing, dup := a.dropHooks[target.Name]; dup {
		a.errorf(fn.Pos(), "duplicate `__drop__` for %s (already declared at %s)", target.Name, existing.Decl.Pos())
		return
	}
	// A `linear` type's destruction is a semantic DECISION (commit or roll back), so the
	// compiler refuses to invoke cleanup implicitly (docs/126 §1). Affine sits below
	// linear: `__drop__` induces the affine tier, it cannot demote the linear one.
	if target.Affine && !target.Droppable {
		a.errorf(fn.Pos(), "`linear struct %s` must be consumed explicitly, so it cannot also declare `__drop__`; use `struct` (the destructor induces affinity on its own) or keep the explicit consuming API", target.Name)
		return
	}
	if a.dropHooks == nil {
		a.dropHooks = map[string]*dropHook{}
	}
	a.dropHooks[target.Name] = &dropHook{TypeName: target.Name, Decl: fn, Sym: sym, FnType: fnType, Struct: target}

	// RULE 1 — declaring `__drop__` induces affinity. Droppable (not linear): the implicit
	// drop discharges the obligation, so an abandoned value is not an error, it is a call.
	target.Affine = true
	target.Droppable = true
	target.DropHook = sym.Name
	if target.Decl != nil {
		target.Decl.Affine = true
		target.Decl.Droppable = true
	}
}

// DropHookSymbol returns the mangled `__drop__` symbol for t, or "" when t is not a drop
// type. This is the backend's entry point: a local whose type answers here gets a
// scope-exit destructor call registered (docs/126 D1).
func DropHookSymbol(t Type) string {
	base, _, _, ok := structLiteralBaseAndBindings(t)
	if !ok || base == nil {
		return ""
	}
	return base.DropHook
}

// dropLocalBinding is one live drop-typed stack local inside the function being
// analyzed, in DECLARATION order. Drops run in reverse declaration order (docs/126 §2),
// so this slice is walked backwards at every exit edge.
type dropLocalBinding struct {
	Sym  *Symbol
	Hook *dropHook
	// Owner keys this value's drop sites: the VarDeclStmt for a local, or the FuncDecl
	// for a moved-in parameter. Both are exactly the node whose effect-grant scope the
	// drop runs in, which is what the docs/126 §3 check needs.
	Owner   ast.Node
	DeclPos lexer.Pos
}

// noteDropLocalDeclaration registers a freshly declared drop-typed local. From here on,
// every exit edge out of the enclosing scope carries an implicit `__drop__` call for it,
// unless the value is moved first.
func (a *Analyzer) noteDropLocalDeclaration(owner ast.Node, sym *Symbol, t Type) {
	if a == nil || sym == nil || owner == nil {
		return
	}
	hook, ok := a.dropHookFor(t)
	if !ok {
		return
	}
	a.currentDropLocals = append(a.currentDropLocals, dropLocalBinding{Sym: sym, Hook: hook, Owner: owner, DeclPos: owner.Pos()})
}

// noteDropParam registers a moved-in drop-typed parameter. A consuming call TRANSFERS the
// obligation (docs/126 §2), so the callee frame owns the value and must release it on the
// way out — without this a value handed to a consuming function would silently leak.
func (a *Analyzer) noteDropParam(fn *ast.FuncDecl, sym *Symbol, t Type) {
	// A destructor must not drop its own receiver: `__drop__` IS that value's death, so
	// arming a drop for `self` inside it would recurse forever.
	if hook, ok := a.dropHookFor(t); ok && hook.Decl == fn {
		return
	}
	a.noteDropLocalDeclaration(fn, sym, t)
}

// dropLocalIsLive reports whether the local still owns its value — i.e. it has NOT been
// moved away. Moves (a consuming call, a `return`, an explicit `__drop__`/`.drop()`) go
// through recordAffineConsumption, so the existing affine tracker IS the drop oracle; no
// parallel liveness model is built here.
func (a *Analyzer) dropLocalIsLive(local dropLocalBinding) bool {
	if a.currentAffineValues == nil {
		return true
	}
	return a.currentAffineValues[affineValueKey{Root: local.Sym}].ConsumedBy == ""
}

// noteImplicitDropEdge records the drops that run on one implicit exit edge — a `return`
// or a `try` propagation. Every drop-typed local still holding a value at this point dies
// here, in reverse declaration order.
//
// This is the docs/126 §2 requirement that drops fire on EVERY exit edge, not just
// fall-through: the `try` case is the one the RFC calls out, because the error path
// leaves the scope with no syntax at the drop point at all.
func (a *Analyzer) noteImplicitDropEdge(edge DropEdge, pos lexer.Pos) {
	if a == nil || len(a.currentDropLocals) == 0 {
		return
	}
	for i := len(a.currentDropLocals) - 1; i >= 0; i-- {
		local := a.currentDropLocals[i]
		if !a.dropLocalIsLive(local) {
			continue
		}
		a.recordDropSite(local, edge, pos)
	}
}

// noteTryPropagationDrops is the `try`-without-fallback edge (docs/126 §2). A bare `try`
// returns the error out of the current function, so every live drop local dies on that
// invisible path.
func (a *Analyzer) noteTryPropagationDrops(pos lexer.Pos) {
	a.noteImplicitDropEdge(DropEdgeTry, pos)
}

func (a *Analyzer) recordDropSite(local dropLocalBinding, edge DropEdge, pos lexer.Pos) {
	site := DropSite{
		ValueName:  local.Sym.Name,
		TypeName:   local.Hook.TypeName,
		HookSymbol: local.Hook.Sym.Name,
		Edge:       edge,
		Pos:        pos,
		DeclPos:    local.DeclPos,
	}
	if a.implicitDropSites == nil {
		a.implicitDropSites = map[ast.Node][]DropSite{}
	}
	// One site per (value, edge kind): the same value can die on several `try` edges, but
	// the effect obligation and the emitted call are identical, so collapse them.
	for _, existing := range a.implicitDropSites[local.Owner] {
		if existing.Edge == edge && existing.ValueName == site.ValueName {
			return
		}
	}
	a.implicitDropSites[local.Owner] = append(a.implicitDropSites[local.Owner], site)
}

// finalizeImplicitDrops closes out a function body: whatever drop locals are still live
// at the end die on normal fall-through.
//
// Effect PROPAGATION is not done here — inferFunctionPermissionEffects re-derives each
// function's effect row from the AST in a whole-program fixpoint, so the drop's effects
// are contributed there (permissionEffectCollector.collectStmt, VarDeclStmt case). Doing
// it here would be silently overwritten by that pass.
func (a *Analyzer) finalizeImplicitDrops() {
	if a == nil || len(a.currentDropLocals) == 0 {
		return
	}
	a.noteImplicitDropEdge(DropEdgeScopeExit, lexer.Pos{})
}

// implicitDropPermissionRefs returns the effects every destructor that can run at this
// declaration's exit edges requires. Feeds the whole-program effect fixpoint so a
// function holding a `File` transparently gains `can[Io.Close]` in its own signature.
func (a *Analyzer) implicitDropPermissionRefs(owner ast.Node) []ast.PermissionRef {
	sites := a.implicitDropSitesForStmt(owner)
	if len(sites) == 0 {
		return nil
	}
	var refs []ast.PermissionRef
	seen := map[string]bool{}
	for _, site := range sites {
		if seen[site.TypeName] {
			continue
		}
		seen[site.TypeName] = true
		if hook, ok := a.dropHooks[site.TypeName]; ok && hook.FnType != nil {
			refs = append(refs, hook.FnType.PermissionRefs...)
		}
	}
	return refs
}

// validateImplicitDropGrants is the docs/126 §3 effect check, run from the permission
// pass so it sees the same `granted` set the author's own calls see. It is keyed by the
// declaring statement because a drop always runs in the declaration's own grant scope.
//
// Unlike an ordinary call's effect-authority diagnostic (a warning), a missing grant here
// is a hard ERROR: the call site is invisible, so a warning would let an unwritten
// `Io.Close` slip through unread.
func (a *Analyzer) validateImplicitDropGrants(owner ast.Node, granted map[string]bool) {
	sites := a.implicitDropSitesForStmt(owner)
	if len(sites) == 0 {
		return
	}
	for _, site := range sites {
		hook, ok := a.dropHooks[site.TypeName]
		if !ok || hook == nil || hook.FnType == nil {
			continue
		}
		refs := a.permissionRefsRequiringLocalGrant(hook.FnType)
		missing := missingGrantedPermissionFamilies(refs, granted)
		if len(missing) == 0 {
			continue
		}
		pos := site.Pos
		if pos == (lexer.Pos{}) {
			pos = site.DeclPos
		}
		label := "drop of " + strconvQuote(site.ValueName) + " on " + site.Edge.String()
		a.errorf(pos, "%s", effectAuthorityGrantMessage(label, missing, permissionGrantHint(refs, missing)))
	}
}

// ImplicitDropSitesForStmt returns the compiler-inserted destructor calls attached to one
// declaration, in the order they were recorded. Exported for the backend, which registers
// a scope-exit cleanup for any declaration that has sites.
func (a *Analyzer) implicitDropSitesForStmt(owner ast.Node) []DropSite {
	if a == nil || owner == nil || a.implicitDropSites == nil {
		return nil
	}
	return a.implicitDropSites[owner]
}

// rewriteExplicitDropCall lowers `value.drop()` into the destructor call it stands for
// (docs/126 §2 "Explicit early release"). It is an ordinary CONSUMING call — the receiver
// is moved into it — so the affine tracker records the consumption and the scope-exit drop
// is statically elided by exactly the same rule that elides it after any other move.
func (a *Analyzer) rewriteExplicitDropCall(expr *ast.CallExpr, fieldExpr *ast.FieldExpr, receiverType Type) bool {
	if a == nil || expr == nil || fieldExpr == nil || fieldExpr.Field != explicitDropMethodName {
		return false
	}
	if len(expr.Args) != 0 {
		return false
	}
	hook, ok := a.dropHookFor(receiverType)
	if !ok || hook.Sym == nil {
		return false
	}
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: hook.Sym.Name}
	expr.Args = []ast.Expr{&ast.MoveExpr{Position: fieldExpr.Object.Pos(), Operand: fieldExpr.Object}}
	expr.ArgNames = nil
	expr.ArgShorthand = nil
	expr.ArgItemOrder = nil
	return true
}

// rejectNonStackDropStorage / rejectDropTypedGlobals keep drop types off non-stack
// storage in D1.

// rejectDropTypedGlobals keeps drop types off static storage in D1: a global never dies,
// so its destructor would never run (and a `mutable` global would double-drop on
// overwrite). Runs right after collectDropHooks, once affinity is known.
func (a *Analyzer) rejectDropTypedGlobals(decls []scopedDecl) {
	if len(a.dropHooks) == 0 {
		return
	}
	for _, scoped := range decls {
		global, ok := scoped.Decl.(*ast.GlobalDecl)
		if !ok || global == nil || global.Type == nil {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			if hook, ok := a.dropHookFor(a.resolveType(global.Type)); ok {
				a.errorf(global.Pos(), "global %q cannot hold drop type %s: a global never dies, so its `__drop__` would never run", global.Name, hook.TypeName)
			}
		})
	}
}

func moduleDisplayName(namespace string) string {
	if namespace == "" {
		return "<root>"
	}
	return namespace
}

// rejectNonStackDropStorage enforces the D1 restriction (docs/126 §7): drop-typed values
// live on the stack or in parameters only. Region/heap placement needs the per-region
// registration lists of D2, so an allocation is rejected with a diagnostic that says so
// rather than silently leaking the destructor.
func (a *Analyzer) rejectNonStackDropStorage(pos lexer.Pos, t Type, what string) {
	hook, ok := a.dropHookFor(t)
	if !ok {
		return
	}
	a.errorf(pos, "%s of drop type %s is not supported yet: a `__drop__` type must live in a stack local or parameter until region drop registration lands (docs/126 D2)", what, hook.TypeName)
}
