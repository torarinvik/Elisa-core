package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// Predicate-fact tracking (docs/85: mutable refinement flow).
//
// The integer range prover (analyzer_refinement_flow.go) is deliberately IMMUTABLE-ONLY: a range
// fact about a mutable variable could be invalidated by a later write, so it is never carried.
// Predicate facts make mutable tracking SOUND by doing the opposite of refusing them — they are
// carried but INVALIDATED at every mutation site. A fact "predicate P holds on x" is gained from a
// flow narrowing (`if x is P:`), used to discharge a downstream obligation on x without a runtime
// check, and dropped the moment x is mutated (assigned, or passed by `&`/mutable to a call). The
// drop is the soundness core: a mutation can break the predicate (e.g. `pop` on a `NonEmpty`
// darray), so the compiler stops trusting the fact exactly where the mutation happens — which is
// the "loses the refinement where it is called" behavior. Dropping is always sound (it only adds
// runtime checks, never removes them); only bare laws (no bracket args) participate for now.

// recordPredFact remembers that bare law-predicate `pred` holds on variable `name` within `scope`.
func recordPredFact(scope *Scope, name, pred string) {
	if scope == nil || name == "" || pred == "" {
		return
	}
	if scope.predFacts == nil {
		scope.predFacts = map[string]map[string]bool{}
	}
	set := scope.predFacts[name]
	if set == nil {
		set = map[string]bool{}
		scope.predFacts[name] = set
	}
	set[pred] = true
}

// recordPredFactWithDeps remembers fact `key` on variable `name`, recording the dependency roots its
// bounds read (docs/85 §5.3). When deps is non-empty the fact is DEPENDENT: it survives only while
// every dep root is frozen, and mutating any of them drops it via invalidatePredFacts.
func recordPredFactWithDeps(scope *Scope, name, key string, deps []string) {
	if scope == nil || name == "" || key == "" {
		return
	}
	recordPredFact(scope, name, key)
	if len(deps) == 0 {
		return
	}
	if scope.predFactDeps == nil {
		scope.predFactDeps = map[string]map[string][]string{}
	}
	byKey := scope.predFactDeps[name]
	if byKey == nil {
		byKey = map[string][]string{}
		scope.predFactDeps[name] = byKey
	}
	byKey[key] = append([]string(nil), deps...)
}

// lookupPredFact reports whether bare law-predicate `pred` is known to hold on `name` anywhere in
// the active scope chain.
func (a *Analyzer) lookupPredFact(name, pred string) bool {
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.predFacts != nil {
			if set := scope.predFacts[name]; set != nil && set[pred] {
				return true
			}
		}
	}
	return false
}

// invalidatePredFacts drops EVERY predicate fact about `name` across the whole active scope chain.
// Called at each mutation site (assignment, ref-call). Deleting upward through parent scopes is the
// sound direction: a real mutation of x invalidates whatever a branch or the enclosing scope
// believed about x, and over-dropping only forces more runtime checks, never fewer. This also gives
// correct merge behavior for free — a fact gained inside one `if` branch and mutated there is gone
// at the merge point.
func (a *Analyzer) invalidatePredFacts(name string) {
	if name == "" {
		return
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.predFacts != nil {
			delete(scope.predFacts, name)
		}
		// Dependence-freeze (docs/85 §5.3): mutating `name` also breaks any OTHER variable's dependent
		// fact whose bounds read `name` (e.g. `i is Bounded[0, name.count]`). Drop those facts and
		// their dependency records so a stale length bound can never discharge an obligation.
		if scope.predFactDeps != nil {
			delete(scope.predFactDeps, name)
			for holder, byKey := range scope.predFactDeps {
				for key, deps := range byKey {
					if containsString(deps, name) {
						delete(byKey, key)
						if scope.predFacts[holder] != nil {
							delete(scope.predFacts[holder], key)
						}
					}
				}
				if len(byKey) == 0 {
					delete(scope.predFactDeps, holder)
				}
			}
		}
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// mutatingBuiltinCollectionMethods are the builtin darray/dict/set methods that change a container's
// contents or layout. Unlike a user method with a `mutable T&` self (whose RefType param drives the
// ref-arg fact invalidation), these are modeled with a VALUE receiver, so a mutation through them
// would otherwise leave a stale flow fact about the receiver. Pure reads (count/get/contains/rows/
// valid/entry) are intentionally excluded: they cannot break a fact, so dropping for them would only
// cost completeness. A stdlib method like `pop` carries a real `T&` self and is already handled.
var mutatingBuiltinCollectionMethods = map[string]bool{
	"push": true, "extend": true, "reserve": true, "resize": true, "clear": true,
	"truncate": true, "insert": true, "remove": true, "set": true, "put": true,
	"add": true, "get_or_insert": true,
}

// mutatingBuiltinMethodReceiver returns the receiver PLACE of a mutating builtin collection method
// call (`xs.clear()` → xs, `b.items.push(v)` → b.items), or ok=false. A mutating builtin is modeled
// with a value receiver, so a call through it is invisible to the ref-arg machinery — both the
// fact-invalidation and the frame-enforcement hooks treat it as a write to this place. Gated to a
// collection-typed receiver so an unrelated user method of the same name is untouched (a real mutable
// `T&` self is already handled by the ref-arg paths).
func (a *Analyzer) mutatingBuiltinMethodReceiver(expr *ast.CallExpr) (ast.Expr, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	field, ok := stripParenExpr(expr.Func).(*ast.FieldExpr)
	if !ok || field == nil || field.Object == nil || !mutatingBuiltinCollectionMethods[field.Field] {
		return nil, false
	}
	if !a.receiverIsBuiltinCollection(field.Object) {
		return nil, false
	}
	return field.Object, true
}

// invalidateFactsForMutatingBuiltinMethod drops every flow fact about the receiver of a mutating
// builtin collection method, closing the hole where such a value-receiver method slipped past the
// ref-arg invalidation (e.g. a `NonEmpty` fact surviving `xs.clear()`). It mirrors the per-channel
// drops the ref-arg path performs (predicate, range, written-const, and SMT assert facts). Over-
// dropping only adds runtime checks, so the conservative drop stays sound.
func (a *Analyzer) invalidateFactsForMutatingBuiltinMethod(expr *ast.CallExpr) {
	recv, ok := a.mutatingBuiltinMethodReceiver(expr)
	if !ok {
		return
	}
	a.invalidatePredFactsForTarget(recv)
	a.invalidateRangeFactsForTarget(recv)
	a.invalidateWrittenConst(rootIdentNameOrEmpty(recv))
	a.invalidateSMTAssertFactsForTarget(recv)
}

// receiverIsBuiltinCollection reports whether an expression's resolved type is a builtin darray/dict/
// set (peeling a ref) — the carriers whose builtin mutating methods this audit covers. An unknown type
// (nil) returns false: the name-set gate alone is then the only filter, which stays sound because the
// fact channels are only consulted for variables that were narrowed in the first place.
func (a *Analyzer) receiverIsBuiltinCollection(obj ast.Expr) bool {
	receiverType := a.exprTypes[obj]
	if receiverType == nil {
		// machine from builds its capture manifest before arm expressions are analyzed.
		// Recover the type from collected symbols/fields so a builtin mutation such as
		// `items.push(v)` is captured during that pre-lowering pass as well.
		receiverType = a.machineFromStaticExprType(obj)
	}
	switch stripRefForBounds(receiverType).(type) {
	case *DArrayType, *DictType, *SetType:
		return true
	}
	return false
}

// invalidatePredFactsForTarget drops predicate facts about the root variable of a mutation target
// expression (an identifier, or a field/index path rooted at one). Conservative: any write under a
// root invalidates the root's facts.
func (a *Analyzer) invalidatePredFactsForTarget(target ast.Expr) {
	for _, name := range a.mutationRootsForTarget(target) {
		a.invalidatePredFacts(name)
	}
}

// mutationRootsForTarget returns every root variable a write to `target` mutates: its structural root
// PLUS any place it ALIASES. A borrow local (`r: mutable T& = &x`) mutated through — `r <- …`,
// `bump(r)`, `r.clear()` — writes the underlying place x, so fact invalidation must drop facts for x
// too; otherwise a fact narrowed on x survives a mutation laundered through r (audit cluster C). Roots
// are deduped; an empty result means the target had no identifiable root (callers treat that as
// "invalidate conservatively").
func (a *Analyzer) mutationRootsForTarget(target ast.Expr) []string {
	seen := map[string]bool{}
	var roots []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			roots = append(roots, name)
		}
	}
	if name, ok := rootIdentName(target); ok {
		add(name)
		for _, r := range a.borrowLaunderedRoots(name, map[string]bool{}) {
			add(r)
		}
	}
	return roots
}

// borrowLaunderedRoots returns the underlying place root(s) that a LOCAL borrow alias points at, read
// from its recorded alias binding (`r: mutable T& = &x` → [x]; transitively through chained borrows).
// Restricted to a SymbolLocal: the broad aliasRootsForExpr also relates ref PARAMETERS (e.g. distinct
// `mutable u64&` out-params) and struct carriers, which would over-invalidate unrelated places. `seen`
// guards against a cyclic binding.
func (a *Analyzer) borrowLaunderedRoots(name string, seen map[string]bool) []string {
	if a.currentScope == nil || name == "" || seen[name] {
		return nil
	}
	seen[name] = true
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || sym.Kind != SymbolLocal {
		return nil
	}
	binding, ok := a.currentAliasBindings[sym]
	if !ok || len(binding.Roots) == 0 {
		return nil
	}
	var out []string
	for _, root := range binding.Roots {
		if root == name {
			continue
		}
		out = append(out, root)
		out = append(out, a.borrowLaunderedRoots(root, seen)...)
	}
	return out
}

// invalidateWrittenConstForLaunderedRoots drops the written-const fact of every place a write to
// `target` reaches THROUGH a borrow-local alias (not the target's own root, which its callers handle).
func (a *Analyzer) invalidateWrittenConstForLaunderedRoots(target ast.Expr) {
	if name, ok := rootIdentName(target); ok {
		for _, r := range a.borrowLaunderedRoots(name, map[string]bool{}) {
			a.invalidateWrittenConst(r)
		}
	}
}

// rootIdentName walks a field/index path down to its root identifier name.
func rootIdentName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.ParenExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Inner)
	case *ast.FieldExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Object)
	case *ast.IndexExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Object)
	case *ast.UnaryExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Operand)
	case *ast.AddrOfExpr:
		if n == nil {
			return "", false
		}
		return rootIdentName(n.Operand)
	default:
		return "", false
	}
}

// recordWrittenConstForTarget records (or clears) the written-constant fact for an assignment
// `target <- value` / `target = value`. For a bare-identifier target whose RHS is a compile-time
// constant of any const-evaluable kind (int, bool, enum, float, char, string), the variable is now
// known to equal that constant expr; any other target shape or a non-constant RHS clears the fact
// (the value is no longer statically known). Sound because the fact is invalidated again at the next
// mutation, `mutable T&` pointees are non-aliased, and the stored RHS references only literals /
// immutable consts (evalConstExpr never resolves a mutable local) so re-evaluation is stable.
func (a *Analyzer) recordWrittenConstForTarget(target, value ast.Expr) {
	// A write laundered through a borrow-local alias (`r := &y; r <- 9`) mutates the underlying place, so
	// its written-const fact (`y == 5`) must drop too — the per-target handling below only touches the
	// alias's own root (audit: writtenConst alias channel, sibling of cluster C).
	a.invalidateWrittenConstForLaunderedRoots(target)
	name, ok := target.(*ast.Ident)
	if !ok || name == nil {
		a.invalidateWrittenConst(rootIdentNameOrEmpty(target))
		return
	}
	// A struct-literal RHS does not const-fold as a whole, but its individual fields may be constants
	// we want to recover later for field-place refinements (`requires off + 4 <= v.size`). Record the
	// literal itself; field extraction const-evaluates per field at use. invalidateWrittenConst (called
	// at every mutation/borrow of `name`) also clears writtenStruct, so this can never go stale.
	if lit, ok := unwrapParen(value).(*ast.StructLitExpr); ok && lit != nil {
		a.invalidateWrittenConst(name.Name)
		if a.currentScope == nil {
			return
		}
		if a.currentScope.writtenStruct == nil {
			a.currentScope.writtenStruct = map[string]*ast.StructLitExpr{}
		}
		a.currentScope.writtenStruct[name.Name] = lit
		return
	}
	if _, ok := a.evalConstExpr(value); !ok {
		a.invalidateWrittenConst(name.Name)
		return
	}
	if a.currentScope == nil {
		return
	}
	// Drop any shadowed parent entry first so the lookup finds this fresh value.
	a.invalidateWrittenConst(name.Name)
	if a.currentScope.writtenConst == nil {
		a.currentScope.writtenConst = map[string]ast.Expr{}
	}
	a.currentScope.writtenConst[name.Name] = value
}

func rootIdentNameOrEmpty(expr ast.Expr) string {
	if name, ok := rootIdentName(expr); ok {
		return name
	}
	return ""
}

// invalidateWrittenConst drops the written-constant fact for a variable across the scope chain,
// mirroring invalidatePredFacts. Called at every mutation site that does not record a new constant.
func (a *Analyzer) invalidateWrittenConst(name string) {
	if name == "" {
		return
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenConst != nil {
			delete(scope.writtenConst, name)
		}
		if scope.writtenStruct != nil {
			delete(scope.writtenStruct, name)
		}
		if scope.writtenListCount != nil {
			delete(scope.writtenListCount, name)
		}
	}
	// Field facts (`self.f <- v`) under this root are dropped via the dedicated root-escape path
	// (invalidateWrittenFieldsForRoot), NOT here: a sibling field write (`self.b <- …`) routes through
	// invalidateWrittenConst(self) for the writtenConst channel but must NOT drop `self.a`'s field fact.
	// Whole-root escapes (reassign, borrow, mutating callee) call invalidateWrittenFieldsForRoot directly.
}

// invalidateWrittenFieldsForRoot drops EVERY field fact whose root is `name` across the scope chain.
// Called when the whole root escapes or is mutated wholesale (reassignment of `self`, a `&self` borrow,
// passing `self` to a mutating callee) — any field could then have changed, so no last-written value
// can be trusted. Over-dropping only forces the runtime check, so this stays sound.
func (a *Analyzer) invalidateWrittenFieldsForRoot(name string) {
	if name == "" {
		return
	}
	prefix := name + "__field__"
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenField == nil {
			continue
		}
		for key := range scope.writtenField {
			if key == name || strings.HasPrefix(key, prefix) {
				delete(scope.writtenField, key)
			}
		}
	}
	// A whole-root invalidation also retires the zeroed-struct-local tracking entry: the zeroed-fields
	// baseline is no longer sound once the root is reassigned, borrowed mutably, or escapes to a
	// mutating callee. Over-dropping is safe — it forces a runtime check rather than a false proof.
	delete(a.zeroedStructLocals, name)
}

// recordWrittenFieldForTarget records (or clears) the last-written value of a struct-field place
// `self.f <- value`. The target must be a pure field path rooted at an identifier (its canonical
// projection name is the map key); any other target shape records nothing. The stored expr is the
// raw RHS — equality discharge is syntactic (`self.f == 0`, or `self.a == self.b` when both fields
// trace to the same value expr), so no const-fold is required. Sound because invalidateWrittenConst
// drops every field fact under a root the moment that root is mutated, aliased, or escapes.
func (a *Analyzer) recordWrittenFieldForTarget(target, value ast.Expr) {
	// A write laundered through a borrow-local alias (`r := &self; r.f <- …` or `r <- …`) mutates the
	// underlying root, so every field fact under each laundered root must drop too.
	if name, ok := rootIdentName(unwrapParen(target)); ok {
		for _, r := range a.borrowLaunderedRoots(name, map[string]bool{}) {
			a.invalidateWrittenFieldsForRoot(r)
		}
	}
	field, ok := unwrapParen(target).(*ast.FieldExpr)
	if !ok || field == nil {
		// A whole-root reassignment (`self = other`) or any non-field target replaces the aggregate, so
		// every field fact under the root is stale. (A bare ident is the common case; conservatively drop
		// the structural root of any other shape too.)
		a.invalidateWrittenFieldsForRoot(rootIdentNameOrEmpty(target))
		return
	}
	root, ok := rootIdentName(field)
	if !ok || root == "" {
		return
	}
	// Only pure ident/field projections get a stable key; smtProjectionName falls back to a position-
	// based name for anything else (an index, a call), which we must not track as a field place.
	key := smtProjectionName(field)
	if !isPureProjectionKey(key) {
		return
	}
	if a.currentScope == nil {
		return
	}
	// Drop any prior fact for THIS field first (a re-write supersedes it); sibling fields are untouched.
	a.invalidateWrittenField(key)
	// Only a re-evaluation-stable value is tracked: a compile-time constant, or a pure place read
	// (ident / field projection — no call, index, or arithmetic). A constant proves `self.f == 0`; a
	// pure place read proves `self.a == self.b` by syntactic equality of the two recorded reads. An
	// impure value (a call) is NOT recorded — the field's prior fact was already dropped above, so the
	// place correctly falls to the runtime check rather than carrying an unstable term.
	if !a.isStableWrittenFieldValue(value) {
		return
	}
	if a.currentScope.writtenField == nil {
		a.currentScope.writtenField = map[string]ast.Expr{}
	}
	a.currentScope.writtenField[key] = value
}

// isStableWrittenFieldValue reports whether an assigned value can be recorded as a field's last-written
// value: a compile-time constant, or a pure place read (ident or field projection whose key is pure).
// Anything that could observe or cause a side effect, or whose re-evaluation could differ (a call,
// arithmetic, an index), is rejected so a recorded fact never carries an unstable term.
func (a *Analyzer) isStableWrittenFieldValue(value ast.Expr) bool {
	if _, ok := a.evalConstExpr(value); ok {
		return true
	}
	switch n := unwrapParen(value).(type) {
	case *ast.Ident:
		return n != nil
	case *ast.FieldExpr:
		return n != nil && isPureProjectionKey(smtProjectionName(n))
	default:
		return false
	}
}

// invalidateWrittenFieldForWrite drops the field fact(s) a write to `target` invalidates WITHOUT
// recording a new value: a pure field-path target drops only that field (siblings stay); any other
// shape (a bare-ident reassign, an index, a non-pure path) drops the whole root's fields. Used by
// writes whose new value is not a tracked field value (augmented assignment, `as &` stores).
func (a *Analyzer) invalidateWrittenFieldForWrite(target ast.Expr) {
	if name, ok := rootIdentName(unwrapParen(target)); ok {
		for _, r := range a.borrowLaunderedRoots(name, map[string]bool{}) {
			a.invalidateWrittenFieldsForRoot(r)
		}
	}
	if field, ok := unwrapParen(target).(*ast.FieldExpr); ok && field != nil {
		key := smtProjectionName(field)
		if isPureProjectionKey(key) {
			a.invalidateWrittenField(key)
			return
		}
	}
	a.invalidateWrittenFieldsForRoot(rootIdentNameOrEmpty(target))
}

// invalidateWrittenField drops a single field place's last-written fact across the scope chain.
func (a *Analyzer) invalidateWrittenField(key string) {
	if key == "" {
		return
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenField != nil {
			delete(scope.writtenField, key)
		}
	}
}

// lookupWrittenField returns the last-written value expr for a field place (keyed by its projection
// name), if a live fact exists in the active scope chain.
func (a *Analyzer) lookupWrittenField(key string) (ast.Expr, bool) {
	if key == "" {
		return nil, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenField != nil {
			if v, ok := scope.writtenField[key]; ok {
				return v, true
			}
		}
	}
	return nil, false
}

// isPureProjectionKey reports whether a projection name was built purely from ident/field components
// (so it round-trips a real field place) rather than smtProjectionName's `expr__…` position fallback.
func isPureProjectionKey(key string) bool {
	return key != "" && !strings.HasPrefix(key, "expr__")
}

// lookupWrittenStructField returns the construction-time value expr of `name.field` when `name` is a
// local known to hold a struct literal (via writtenStruct) AND the field was given by name in that
// literal. Positional or absent fields decline (sound: no proof, falls to the runtime check).
func (a *Analyzer) lookupWrittenStructField(name, field string) (ast.Expr, bool) {
	if name == "" || field == "" {
		return nil, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenStruct == nil {
			continue
		}
		lit, ok := scope.writtenStruct[name]
		if !ok || lit == nil {
			continue
		}
		for i := range lit.Args {
			if lit.ArgName(i) == field {
				return lit.Args[i], true
			}
		}
		return nil, false
	}
	return nil, false
}

// recordWrittenListCount records, for an immutable local initialised from a list literal, the number
// of elements as a compile-time fact.  Only call for immutable bindings; mutable darrays must not
// be recorded because push/pop invalidation is not tracked here.
func (a *Analyzer) recordWrittenListCount(name string, count int64) {
	if name == "" || a.currentScope == nil {
		return
	}
	if a.currentScope.writtenListCount == nil {
		a.currentScope.writtenListCount = map[string]int64{}
	}
	a.currentScope.writtenListCount[name] = count
}

// lookupWrittenListCount returns the known element count of an immutable darray local whose
// initialiser was a list literal, searching the scope chain.  Used by affineOf / substitutedAffine
// to resolve `xs.count` in parametric alias preconditions.
func (a *Analyzer) lookupWrittenListCount(name string) (int64, bool) {
	if name == "" {
		return 0, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenListCount != nil {
			if n, ok := scope.writtenListCount[name]; ok {
				return n, true
			}
		}
	}
	return 0, false
}

// lookupWrittenConst returns the known constant-value expr of a variable, if a written-constant fact
// for it is live in the active scope chain. The expr is the original const RHS (any kind), suitable
// for use as a const-eval subject.
func (a *Analyzer) lookupWrittenConst(name string) (ast.Expr, bool) {
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.writtenConst != nil {
			if v, ok := scope.writtenConst[name]; ok {
				return v, true
			}
		}
	}
	return nil, false
}

// callPreservesArgRefinements reports whether passing an argument as ref-param `paramIndex` of
// `call` (callee type `appliedType`) provably leaves the argument's refinements intact, so the
// caller can KEEP its predicate/written-const facts across the call (docs/85 brick 3, preserve
// credit). Two sound signals: (1) the callee declares `ensures <param> preserve` (a
// FuncPoststateKindPreserve over the whole target — its ParamIndex aligns with the call loop index,
// same as funcPoststatesForParam); (2) the parameter is an IMMUTABLE borrow (the callee decl's
// ParamDecl.Mutable is false), so the callee cannot write through it and Elisa's borrow rules
// forbid a concurrent mutable alias. Anything unprovable returns false → the brick-1 drop stands.
func (a *Analyzer) callPreservesArgRefinements(call *ast.CallExpr, appliedType *FuncType, paramIndex int) bool {
	if appliedType != nil {
		for _, ps := range appliedType.Poststates {
			if ps.ParamIndex == paramIndex && ps.Kind == FuncPoststateKindPreserve && len(ps.Path) == 0 {
				return true
			}
		}
		// An IMMUTABLE borrow `T&` (non-mutable pointee) cannot be written through, so the argument's
		// refinements survive. Reading this from the resolved FuncType — rather than the call's decl —
		// makes the signal work for EVERY call shape (method `obj.m(arg)`, indirect/closure calls),
		// not just direct by-name calls. Index aligns with the call's param loop, same as Poststates.
		if paramIndex >= 0 && paramIndex < len(appliedType.Params) {
			if rt, ok := appliedType.Params[paramIndex].(*RefType); ok && rt != nil && !rt.Mutable {
				return true
			}
		}
	}
	if decl, ok := a.resolveDirectCallFuncDecl(call); ok && decl != nil {
		if paramIndex >= 0 && paramIndex < len(decl.Params) && paramIsImmutableBorrow(decl.Params[paramIndex]) {
			return true
		}
	}
	return false
}

// paramIsImmutableBorrow reports whether a parameter is an immutable borrow `T&` — a ref through
// which the callee cannot write, so an argument's refinements survive the call. The canonical
// mutable borrow is `p: mutable i64&` (a MutableType wrapping the ref); the legacy form is a leading
// `mutable` keyword (ParamDecl.Mutable). Both are rejected here; only a plain `*ast.RefType` whose
// pointee is not itself mutable counts as immutable. Conservative: any non-ref or mutable shape is
// not a preserving borrow.
func paramIsImmutableBorrow(p ast.ParamDecl) bool {
	if p.Mutable {
		return false
	}
	rt, ok := p.Type.(*ast.RefType)
	if !ok || rt == nil {
		return false
	}
	if _, mut := rt.Elem.(*ast.MutableType); mut {
		return false
	}
	return true
}

// factKey builds the canonical predicate-fact key for a law applied to constant arguments. A bare
// law is just its name; a parametric law `Bounded[0, 500]` whose args are all compile-time integer
// constants is `Bounded[0,500]`. Returns false when any argument is not a constant integer (such a
// fact can't be matched against an obligation soundly, so it is not tracked). Keying by exact arg
// values means a fact only discharges an obligation with the SAME bounds — `Bounded[0,500]` never
// proves `Bounded[10,20]` (no cross-bounds entailment here; that is the range prover's job).
func (a *Analyzer) factKey(lawName string, argExprs []ast.Expr) (string, bool) {
	if lawName == "" {
		return "", false
	}
	if len(argExprs) == 0 {
		return lawName, true
	}
	key := lawName + "["
	for i, arg := range argExprs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return "", false
		}
		if i > 0 {
			key += ","
		}
		key += strconv.FormatInt(c, 10)
	}
	return key + "]", true
}

// factKeyWithDeps builds a predicate-fact key for a law whose args may be CONSTANT or DEPENDENT
// (docs/85 §5.3 dependence-freeze). A constant integer arg is keyed by value (exact-match, as
// factKey). A dependent arg — a pure, side-effect-free path/arithmetic over variables (`xs.count`,
// `xs.count - 1`, `r.vw`) — is keyed by its canonical text, and the root variables it reads are
// returned as `deps`; such a fact is sound only while those roots are frozen (a mutation of any of
// them invalidates it). Returns ok=false for any non-freezable arg (a call, an impure or
// unrecognized expr) so the fact is simply not tracked. deps is nil for an all-constant fact, making
// this a superset of factKey with identical keys in the constant case.
func (a *Analyzer) factKeyWithDeps(lawName string, argExprs []ast.Expr) (string, []string, bool) {
	if lawName == "" {
		return "", nil, false
	}
	if len(argExprs) == 0 {
		return lawName, nil, true
	}
	var deps []string
	seen := map[string]bool{}
	key := lawName + "["
	for i, arg := range argExprs {
		if i > 0 {
			key += ","
		}
		if c, ok := a.constIntValue(arg); ok {
			key += strconv.FormatInt(c, 10)
			continue
		}
		if !isFreezableDependentArg(arg) {
			return "", nil, false
		}
		key += unparse.FormatExpr(arg)
		collectFreezableArgRoots(arg, seen, &deps)
	}
	return key + "]", deps, true
}

// isFreezableDependentArg reports whether a dependent-bound argument is a pure, re-evaluation-stable
// path/arithmetic expression: identifiers, field paths (incl. `.count`), constant-or-path indexing,
// integer literals, parens, unary +/-/~, and arithmetic binary ops over such operands. Anything that
// could observe or cause a side effect (a call) or that this checker doesn't recognize is rejected,
// so a fact is tracked only when its dependence on frozen variables is explicit.
func isFreezableDependentArg(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n != nil
	case *ast.IntLit:
		return true
	case *ast.ParenExpr:
		return n != nil && isFreezableDependentArg(n.Inner)
	case *ast.FieldExpr:
		return n != nil && isFreezableDependentArg(n.Object)
	case *ast.IndexExpr:
		return n != nil && isFreezableDependentArg(n.Object) && isFreezableDependentArg(n.Index)
	case *ast.UnaryExpr:
		return n != nil && isFreezableDependentArg(n.Operand)
	case *ast.BinaryExpr:
		if n == nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT:
			return isFreezableDependentArg(n.Left) && isFreezableDependentArg(n.Right)
		}
		return false
	default:
		return false
	}
}

// collectFreezableArgRoots adds the root variable names a freezable dependent arg reads (the bases of
// its paths and the operands of its arithmetic) into deps, deduped via seen.
func collectFreezableArgRoots(expr ast.Expr, seen map[string]bool, deps *[]string) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n != nil && !seen[n.Name] {
			seen[n.Name] = true
			*deps = append(*deps, n.Name)
		}
	case *ast.ParenExpr:
		if n != nil {
			collectFreezableArgRoots(n.Inner, seen, deps)
		}
	case *ast.FieldExpr:
		if n != nil {
			collectFreezableArgRoots(n.Object, seen, deps)
		}
	case *ast.IndexExpr:
		if n != nil {
			collectFreezableArgRoots(n.Object, seen, deps)
			collectFreezableArgRoots(n.Index, seen, deps)
		}
	case *ast.UnaryExpr:
		if n != nil {
			collectFreezableArgRoots(n.Operand, seen, deps)
		}
	case *ast.BinaryExpr:
		if n != nil {
			collectFreezableArgRoots(n.Left, seen, deps)
			collectFreezableArgRoots(n.Right, seen, deps)
		}
	}
}

// gatherLawIsPredFact records a predicate fact for `if x is Law:` / `if x is Law[consts]:` where x
// is a bare identifier — regardless of whether x is mutable. Mutable is safe here because any later
// mutation of x invalidates the fact via invalidatePredFacts. The integer range path
// (gatherLawIsRangeRefinement) handles the decidable `self OP const` fragment over immutable ints;
// this complements it for laws outside that fragment (e.g. `darray is NonEmpty`, body `self.count >
// 0`), for mutable subjects, and for parametric laws applied to constant bounds.
func (a *Analyzer) gatherLawIsPredFact(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if !truthy || scope == nil || n == nil {
		return
	}
	ident, ok := n.Left.(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	targets := flattenIsTargetExprs(n.Right)
	if len(targets) != 1 {
		return
	}
	lawName, lawArgs, ok := a.resolveLawIsTarget(targets[0])
	if !ok {
		return
	}
	key, deps, ok := a.factKeyWithDeps(lawName, lawArgs)
	if !ok {
		return // non-constant, non-freezable parametric args can't be tracked soundly
	}
	// A dependent fact (deps non-empty, docs/85 §5.3) also drops when any frozen dependency mutates.
	recordPredFactWithDeps(scope, ident.Name, key, deps)
}

// tryProveRefinementByFactSet attempts to discharge `value is law[args]` from a tracked predicate
// fact: when value is a bare identifier with a live fact for the SAME (law, constant args) — gained
// from a flow narrowing and not since invalidated by a mutation — the obligation is proven with no
// runtime check.
func (a *Analyzer) tryProveRefinementByFactSet(value ast.Expr, lawName string, predArgs []ast.Expr, allowDependentFacts bool) bool {
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	// Build the key the same way the narrowing site recorded it (constant OR dependent §5.3). A live
	// dependent fact has already survived the freeze invariant — any mutation of its deps would have
	// dropped it — so a hit discharges the obligation with no runtime check.
	key, deps, ok := a.factKeyWithDeps(lawName, predArgs)
	if !ok {
		return false
	}
	// A dependent key names variables; matching one across a function boundary (a call argument, whose
	// callee param refinement spells names in the CALLEE's scope) would conflate distinct variables
	// that happen to share a spelling. Dependent facts therefore discharge only same-function
	// obligations (var decl, return, ensures); constant facts (no deps) are absolute and always match.
	if len(deps) != 0 && !allowDependentFacts {
		return false
	}
	return a.lookupPredFact(ident.Name, key)
}
