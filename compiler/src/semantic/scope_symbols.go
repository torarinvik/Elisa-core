package semantic

import (
	"maps"

	"elisacore/src/ast"
)

type SymbolKind string

const (
	SymbolConst      SymbolKind = "const"
	SymbolGlobal     SymbolKind = "global"
	SymbolFunc       SymbolKind = "func"
	SymbolExternFunc SymbolKind = "extern-func"
	SymbolExternVar  SymbolKind = "extern-var"
	SymbolStruct     SymbolKind = "struct"
	SymbolExternType SymbolKind = "extern-type"
	SymbolParam      SymbolKind = "param"
	SymbolLocal      SymbolKind = "local"
	SymbolRegion     SymbolKind = "region"
	SymbolRegionMark SymbolKind = "region-mark"
	SymbolCheckpoint SymbolKind = "checkpoint"
)

type Symbol struct {
	Name       string
	Kind       SymbolKind
	Type       Type
	Node       ast.Node
	LinkName   string
	AliasOf    *Symbol
	ParamIndex int
	Mutable    bool
	// BindingMutabilityExplicit means the local's binding qualifier was written
	// separately from its type (`mutable p: T&` / `const p: mutable T&`). A
	// mutable binding must not thereby gain write-through capability.
	BindingMutabilityExplicit bool
	Private                   bool
	// Ghost marks a verification-only local introduced by `ghost x: T = expr`. It may be read in
	// contracts (requires/ensure/invariant/assert) but NEVER by real runtime code, and no real
	// value may be assigned from it. Enforced in analyzer_ghost.go; the decl is erased in codegen.
	Ghost bool
	// Deprecated, when non-empty, is the `@deprecated("...")` message; calling this
	// function emits a deprecation diagnostic at the use site.
	Deprecated string
	// HomeRegion, on a by-value struct local that owns region-less container fields, names
	// the shortest region its buffers can live in: the ambient region at the declaration,
	// narrowed on every later write through the local. A struct TYPE never carries its
	// fields' regions, so without this a view into `local.buf` had no provenance at all.
	HomeRegion string
}

func symbolAliasRoot(sym *Symbol) *Symbol {
	for sym != nil && sym.AliasOf != nil {
		sym = sym.AliasOf
	}
	return sym
}

type Scope struct {
	Parent *Scope
	// returnBorrowFrame, inherited by child scopes, observes each successful Define (the return-borrow
	// store environment's binder census, analyzer_return_borrow_store_env.go).
	returnBorrowFrame *returnBorrowDeferFrame
	Symbols           map[string]*Symbol
	// SMT immutable-local reverse indexes are populated by the analyzer only after a real local
	// definition succeeds. Candidates keep nil-root invalidation scoped to immutable VarDeclStmt
	// bindings; the name buckets let finite-root invalidation avoid scanning unrelated locals.
	// Registration is append-only because declarations can be added after the first write.
	smtLocalDefinitionCandidates []*Symbol
	smtLocalDefinitionsByName    map[string][]*Symbol
	smtLocalDefinitionIndexBuilt bool
	// symbolsVersion changes whenever Symbols gains, loses or replaces an entry, or an entry's
	// Type is reassigned; caches derived from a scope's symbols are valid while it is unchanged.
	symbolsVersion uint64
	// Deferred borrow checks share a flattened symbol map until a check
	// defines a symbol in its private scope.
	sharedSymbols bool
	Refinements   map[string]Type
	// narrowedOptionals records, for a place whose refinement narrowed `T?` down to `T`
	// (recordAssignmentRefinement, after `x <- 5`), the DECLARED optional type. The
	// narrowing is a useful fact -- `x` reads as a plain T afterwards -- but it must not
	// take away the ability to ASK about absence: the storage still carries its present
	// flag, so `x == null` and `if x is v:` stay meaningful (and answer statically).
	// Without this they were rejected outright, which is what made an optional common
	// unusable after its first write. A nil value shadows an outer scope's entry.
	narrowedOptionals       map[string]Type
	ConditionalBindingHints map[string]string
	// rangeFacts holds known integer bounds for IMMUTABLE variables within this scope, gathered
	// from a branch condition (`if a > 5` → a ∈ [6,∞)). Used by the refinement flow prover (docs/85
	// 1d-2) to discharge value refinements statically. Immutable-only, so no invalidation is needed.
	rangeFacts map[string]numRange
	// predFacts holds bare law-predicates known to hold on a variable within this scope (var name →
	// set of law names), gained from a flow narrowing (`if x is P:`). Unlike rangeFacts these MAY
	// cover mutable variables, because they are invalidated at every mutation site (docs/85: mutable
	// refinement flow — see analyzer_refinement_predfacts.go).
	predFacts map[string]map[string]bool
	// predFactDeps records, for a DEPENDENT predicate fact (docs/85 §5.3 dependence-freeze), the root
	// variables its bounds read — keyed holder name → fact key → dependency roots. A fact such as
	// `i is Bounded[0, xs.count]` is sound only while `xs` is frozen, so mutating ANY dependency root
	// invalidates the fact through the same chokepoint that drops the holder's own facts
	// (invalidatePredFacts). Constant-only facts have no entry here.
	predFactDeps map[string]map[string][]string
	// writtenConst records the last value written to a variable when that value is a compile-time
	// constant of ANY const-evaluable kind — int (`x <- 0`, `n = 5`), bool (`ready <- true`), enum
	// (`state <- Door.Open`), float/char/string — including writes through a non-aliased `mutable T&`
	// param's pointee. The stored expr IS the original const RHS, which keeps its resolved entry in
	// a.exprTypes, so re-evaluating it (e.g. an enum/bool law application) reproduces the same value.
	// Used to prove/refute a refinement obligation on the variable (e.g. an `ensures p is Positive`
	// postcondition after `p <- 1`, or `ensures d is Open` after `d <- Door.Open`). Invalidated at
	// every mutation site alongside predFacts, so it can never go stale; const-only RHS (no mutable
	// reference) keeps re-evaluation stable.
	writtenConst map[string]ast.Expr
	// writtenStruct holds, for a bare-identifier local assigned a struct literal `v = T{f: <const>}`,
	// that literal — so a refinement/precondition over a field place (`requires off + 4 <= v.size`) can
	// resolve `v.size` to its construction-time constant. Routed through the SAME invalidation path as
	// writtenConst (invalidateWrittenConst), so a field write, reassignment, or mutable-borrow escape of
	// `v` drops it; it can never go stale.
	writtenStruct map[string]*ast.StructLitExpr
	// writtenListCount records, for an immutable local whose initializer is a darray list literal
	// `xs: darray[T] = [v1, v2, …]`, the element count as a compile-time integer.  Used to prove
	// preconditions of the form `self < xs.count` that arise from parametric refine aliases
	// (e.g. `IndexOf[xs]` desugars to `i64 where self >= 0 and self < xs.count`).  Invalidated
	// alongside writtenConst/writtenStruct via invalidateWrittenConst so it can never go stale.
	writtenListCount map[string]int64
	// writtenField records the last value written to a STRUCT-FIELD place via `self.f <- value`
	// (`root.field`), keyed by the field path's canonical projection name (smtProjectionName, e.g.
	// `self.pool_budget`). Used to discharge an `ensure self.f == E` postcondition over fields
	// the void body writes — proving `self.f == 0` directly and `self.a == self.b` when both fields
	// trace to the same value expr — which the SMT lane otherwise fails because two loads of a
	// reference field are distinct opaque terms. Routed through the SAME root-keyed invalidation as
	// writtenConst (invalidateWrittenConst drops every field fact whose root matches), so a re-write,
	// reassignment, mutable-borrow escape, or pass to a mutating callee of the root drops it; it can
	// never go stale.
	writtenField map[string]ast.Expr
	// smtAssertFacts holds scoped proof facts known to hold after a branch, proven assertion,
	// invariant, or exact assignment. They are consumed only by the SMT tier and invalidated when a
	// later mutation touches one of their dependency roots; calls still clear them conservatively.
	// Snapshots may share the backing array. Append normally; removals must
	// use dropSMTFactsCopyOnWrite rather than overwrite array elements.
	smtAssertFacts []smtFact
	// closedWorld marks this scope as a proof WALL (docs/99 scoped proof automation). When a fact-
	// gathering chain walk reaches a closed-world scope it includes that scope's own facts and then
	// STOPS — it does not ascend to the parent. The effect: a `proof:`/`assert … by scoped:` block runs
	// in a closed world over only the facts cited INSIDE the block (lemma ensures recorded here), so the
	// proof is STABLE — unrelated ambient flow/assert facts in enclosing scopes cannot reach the solver,
	// hence cannot change the verdict. Symbol/refinement LOOKUP still ascends past the wall (so lemma and
	// type names resolve); only fact gathering is walled. Soundness is preserved because a wall only
	// REMOVES hypotheses: a goal proven from a strict subset of the available facts is a fortiori proven
	// from all of them, and an omitted necessary fact makes the goal correctly DECLINE.
	closedWorld bool
}

func NewScope(parent *Scope) *Scope {
	var frame *returnBorrowDeferFrame
	if parent != nil {
		frame = parent.returnBorrowFrame
	}
	// Refinements, narrowedOptionals and ConditionalBindingHints stay nil until first written
	// (setRefinement, SetNarrowedOptional, setConditionalBindingHint): most scopes never write
	// them, and three empty maps per scope were most of NewScope's cost.
	return &Scope{Parent: parent, returnBorrowFrame: frame, Symbols: map[string]*Symbol{}}
}

func (s *Scope) Define(sym *Symbol) (*Symbol, bool) {
	if existing, ok := s.Symbols[sym.Name]; ok {
		return existing, false
	}
	if s.sharedSymbols {
		s.Symbols = maps.Clone(s.Symbols)
		s.sharedSymbols = false
	}
	s.Symbols[sym.Name] = sym
	s.symbolsVersion++
	if s.returnBorrowFrame != nil {
		s.returnBorrowFrame.noteBinder(sym)
	}
	return sym, true
}

func (s *Scope) Lookup(name string) (*Symbol, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if sym, ok := cur.Symbols[name]; ok {
			return sym, true
		}
	}
	return nil, false
}

func (s *Scope) LookupRefinement(key string) (Type, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if t, ok := cur.Refinements[key]; ok {
			return t, true
		}
	}
	return nil, false
}

// LookupNarrowedOptional returns the DECLARED optional type of a place that an
// assignment narrowed to its payload, or nil. See Scope.narrowedOptionals.
func (s *Scope) LookupNarrowedOptional(key string) (Type, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if t, ok := cur.narrowedOptionals[key]; ok {
			return t, t != nil
		}
	}
	return nil, false
}

func (s *Scope) setRefinement(key string, t Type) {
	if s.Refinements == nil {
		s.Refinements = map[string]Type{}
	}
	s.Refinements[key] = t
}

func (s *Scope) setConditionalBindingHint(name, hint string) {
	if s.ConditionalBindingHints == nil {
		s.ConditionalBindingHints = map[string]string{}
	}
	s.ConditionalBindingHints[name] = hint
}

func (s *Scope) SetNarrowedOptional(key string, declared Type) {
	if s == nil || key == "" {
		return
	}
	if s.narrowedOptionals == nil {
		s.narrowedOptionals = map[string]Type{}
	}
	s.narrowedOptionals[key] = declared
}

func (s *Scope) LookupConditionalBindingHint(name string) (string, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if hint, ok := cur.ConditionalBindingHints[name]; ok {
			return hint, true
		}
	}
	return "", false
}
