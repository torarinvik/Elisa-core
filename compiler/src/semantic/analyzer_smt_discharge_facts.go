package semantic

import (
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) canAssumeContractFact(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if a.proveRequiresClause(expr, nil) == requiresProven {
		return true
	}
	if proven, _ := a.trySMTProveRequiresReported(expr, nil); proven {
		return true
	}
	a.proofLint(expr.Pos(), "invariant could not be proven statically at this point; keeping the debug runtime check but not using it as a release proof fact")
	return false
}

// smtImmutableLocalHypotheses asserts the defining equality of every immutable integer
// local in scope (`rem: u64 = value % alignment` -> `(assert (= rem (mod value alignment)))`),
// so the prover can reason THROUGH locals instead of treating them as unconstrained free
// variables (docs/85 gap #2). Sound: an immutable local equals its initializer wherever it
// is in scope, and it is never reassigned. A definition outside the integer fragment (a call,
// a float) is skipped — fewer hypotheses only declines a proof, never admits an unsound one.
type smtGlobalLocalNameCache struct {
	scope   *Scope
	version uint64
	names   []string
}

// The parallel body's global symbol kinds and mutability are fixed at fork.
// Each worker owns this cache; a changed scope or definition version rebuilds it.
func (a *Analyzer) smtGlobalImmutableLocalNames(sc *Scope) []string {
	cache := &a.smtGlobalLocalNames
	if cache.scope == sc && cache.version == sc.symbolsVersion {
		return cache.names
	}
	var names []string
	for name, sym := range sc.Symbols {
		if sym != nil && !sym.Mutable && sym.Kind == SymbolLocal {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	*cache = smtGlobalLocalNameCache{scope: sc, version: sc.symbolsVersion, names: names}
	return names
}

func (a *Analyzer) smtImmutableLocalHypotheses(tr *smtTranslator) string {
	if a == nil || a.currentScope == nil || tr == nil {
		return ""
	}
	if a.inClosedWorldProof {
		return "" // docs/99: immutable-local defining equalities are ambient; walled out of a scoped proof.
	}
	var b strings.Builder
	seen := map[string]bool{}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		// Sorted name order so the hypothesis text (and the brick-5 cache key) is deterministic — Symbols
		// is a map, and its iteration order would otherwise vary the query string between identical goals.
		// Filter before sorting: outer (module/global) scopes hold thousands of symbols and no
		// immutable locals, and sorting all of them on every query was ~9 s of a stage1 build.
		// The filter is exactly the per-name skip below, so the emitted hypotheses are unchanged.
		var names []string
		if a.parallel != nil && sc == a.globalScope {
			names = a.smtGlobalImmutableLocalNames(sc)
		} else {
			for name, sym := range sc.Symbols {
				if sym == nil || sym.Mutable || sym.Kind != SymbolLocal || seen[name] {
					continue
				}
				names = append(names, name)
			}
			sort.Strings(names)
		}
		for _, name := range names {
			if seen[name] {
				continue
			}
			sym := sc.Symbols[name]
			seen[name] = true // a closer scope's binding shadows an outer one
			vd, ok := sym.Node.(*ast.VarDeclStmt)
			if ok && vd != nil && vd.Value != nil && sym.Type != nil && !IsNumericType(sym.Type) {
				a.smtAggregateCallLocalEnsures(tr, sc, sym, vd)
			}
			if !ok || vd == nil || vd.Value == nil || sym.Type == nil || !IsNumericType(sym.Type) {
				continue
			}
			a.smtCallLocalResultOnlyEnsures(tr, &b, name, sym, vd.Value)
			// The initializer is translated with the proof point's names: skip it when any of them
			// (or the local itself, `y: i64 = y + 1`) now denotes a different binding.
			if !a.immutableLocalDefinitionCurrent(sc, sym, vd.Value) {
				continue
			}
			eterm, ok := tr.termEnv(a.implicitIntConversion(vd.Value, a.exprType(vd.Value), sym.Type), nil)
			if ok {
				tr.decls[name] = true
				b.WriteString("(assert (= " + smtVar(name) + " " + eterm + "))\n")
			}
			// A `name = X % C` local is bounded 0 <= name < C for ANY X when C > 0 and name is
			// unsigned — independent of whether X itself translates to an SMT term. The left operand
			// may carry a width conversion or shift (e.g. `(seed.u64() >> 16) % C`) that termEnv cannot
			// model, which previously dropped the whole local (and with it this bound). Seed the modulo
			// bound directly so a refinement like VmInRange[0, C-1] on the value still discharges. Gated
			// to unsigned `name` (where 0 <= X%C < C holds unconditionally) and a provably-positive C.
			if bin, isBin := stripOptimizationParens(vd.Value).(*ast.BinaryExpr); isBin && bin.Op == lexer.TOKEN_PERCENT {
				if signed, _, sok := smtIntWidthSign(sym.Type); sok && !signed {
					if rterm, rok := tr.termEnv(bin.Right, nil); rok && a.provablyPositive(bin.Right) {
						tr.decls[name] = true
						b.WriteString("(assert (>= " + smtVar(name) + " 0))\n")
						b.WriteString("(assert (< " + smtVar(name) + " " + rterm + "))\n")
					}
				}
			}
		}
	}
	return b.String()
}

// smtIntWidthSign resolves an integer type to (signedness, bit-width) for the value-preserving
// conversion check, including the pointer-width aliases BitIntInfo does not parse (usize/uintptr
// are unsigned 64-bit, isize/int are signed 64-bit on the targets we emit).
func smtIntWidthSign(t Type) (signed bool, bits int, ok bool) {
	t = stripRefForBounds(t)
	if s, b, k := BitIntInfo(t); k {
		return s, b, true
	}
	if bt, isB := t.(*BuiltinType); isB {
		switch bt.Name {
		case "usize", "uintptr":
			return false, 64, true
		case "isize", "int":
			return true, 64, true
		}
	}
	return false, 0, false
}

// castOperandResolvedType returns the operand type of a cast, falling back — when the per-node
// exprTypes entry is unset (a synthetic/cloned node from measure substitution) — to resolving a
// field-read operand from its struct type, a bare-identifier operand from the scope, or an immutable
// SView byte read from its root. This mirrors the array/scalar field-read fallbacks so a
// `field.usize()` or `view[index].u8()` cast in a cloned expression resolves the same way its
// analyzed twin does.
func (a *Analyzer) castOperandResolvedType(n *ast.CastExpr) Type {
	if n == nil || n.Operand == nil {
		return nil
	}
	if t := a.exprType(n.Operand); t != nil {
		return t
	}
	switch op := n.Operand.(type) {
	case *ast.FieldExpr:
		if t, ok := a.fieldReadResolvedType(op); ok {
			return t
		}
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(op.Name); ok && sym != nil {
				return sym.Type
			}
		}
	case *ast.IndexExpr:
		// Cloned immutable-local equations do not carry per-node expression types. Recover the
		// source type only for the same stable SView reads admitted by the SMT byte model: checked
		// `view[i]` produces char, while raw `view.data[i]` loads u8. In particular, do not infer
		// that an arbitrary index expression is byte-sized.
		if _, raw, ok := (&smtTranslator{a: a}).stableSviewByteIndexRoot(op, nil); ok {
			if raw {
				return &BuiltinType{Name: "u8"}
			}
			return &BuiltinType{Name: "char"}
		}
	case *ast.ParenExpr:
		return a.castOperandResolvedType(&ast.CastExpr{Operand: op.Inner})
	}
	return nil
}

// Callee environments bind values, not the caller's same-spelled source types. Cloned callee
// operands without recorded types must decline rather than borrowing a caller local's width.
func (tr *smtTranslator) castOperandType(n *ast.CastExpr) Type {
	if n == nil {
		return nil
	}
	if tr.calleeBodyDepth == 0 {
		return tr.a.castOperandResolvedType(n)
	}
	operand := n.Operand
	for operand != nil {
		if typ := tr.a.exprType(operand); typ != nil {
			return typ
		}
		paren, ok := operand.(*ast.ParenExpr)
		if !ok {
			break
		}
		operand = paren.Inner
	}
	return nil
}

// postfixCastTargetWidthSign derives the destination integer width/sign of a value-form numeric
// postfix cast (`x.usize()`, `x.u32()`) directly from its TARGET NAME, for cloned cast nodes whose
// result type was never recorded. Only the postfix-shorthand origin with a plain integer target name
// qualifies — exactly the family isPureNumericValueCast admits — so this never widens what counts as a
// modelable cast; it only recovers the type the analyzer would have attached. Declines otherwise.
func postfixCastTargetWidthSign(n *ast.CastExpr) (bool, int, bool) {
	if !isPureNumericValueCast(n) {
		return false, 0, false
	}
	named, ok := n.Target.(*ast.NamedType)
	if !ok || named == nil {
		return false, 0, false
	}
	switch named.Name {
	case "i8", "s8":
		return true, 8, true
	case "i16", "s16":
		return true, 16, true
	case "i32", "s32":
		return true, 32, true
	case "i64", "s64", "isize", "int":
		return true, 64, true
	case "u8":
		return false, 8, true
	case "u16":
		return false, 16, true
	case "u32":
		return false, 32, true
	case "u64", "usize", "uintptr":
		return false, 64, true
	}
	return false, 0, false
}

func smtNumericValueType(t Type) (Type, bool) {
	t = stripRefForBounds(t)
	if enumeration, ok := t.(*ConstEnumType); ok {
		t = enumeration.Storage
	}
	if t == nil || !IsNumericType(t) || IsFloatType(t) {
		return nil, false
	}
	return t, true
}

func smtTypeNonNegative(t Type) bool {
	signed, _, ok := smtIntWidthSign(t)
	return ok && !signed
}

// lawBodyExpr extracts a law's single `return <bool-expr>` body (the decidable shape).
func (a *Analyzer) lawBodyExpr(decl *ast.FuncDecl) (ast.Expr, bool) {
	if decl == nil || len(decl.Body) != 1 {
		return nil, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return nil, false
	}
	return ret.Value, true
}

// smtTranslator lowers the integer/bool expression fragment to SMT-LIB2, collecting the free
// variables it declares so their flow facts can be asserted as hypotheses.
type smtTranslator struct {
	reusePreamble   string // single-use handoff from smtCheckVCMulti; see takeFactPreamble
	reusePreambleOK bool
	stringBindings  map[string]smtConstantStringBinding // immutable bindings selected by each substitution environment
	// String-view classifier params are represented by fresh, unconstrained Ints. Each
	// literal observed in this query gets a distinct integer ID, preserving exact equality
	// and exclusivity against known literals without modeling arbitrary string contents.
	stringParamInts      map[smtStringParameterKey]string
	stringLiteralInts    map[string]int64
	stringFieldIDs       map[string]string
	nextStringLiteralInt int64
	a                    *Analyzer
	decls                map[string]bool // Elisa ident -> declared as an SMT Int const
	arrayDecls           map[string]bool // Elisa ident -> declared as an SMT (Array Int Int) (docs/90 brick 90-5)
	byteReadBounds       map[string]bool // stable byte-select terms whose unsigned range was emitted
	// setDecls / dictDecls model set[T] / dict[K,V] containers. A set is an (Array <KSort> Bool)
	// membership function (`p in s` = `(select s p)`). A dict is modeled as TWO arrays keyed by the
	// same KSort: a `<sym>_keys` (Array <KSort> Bool) membership predicate plus a `<sym>_vals`
	// (Array <KSort> Int) value map (`k in d` = `(select d_keys k)`, `d[k]` = `(select d_vals k)`).
	// The map value is the SMT sort string for the key/element ("Int" or "Bool"). Only int/bool key
	// and int value types are modeled; floats/structs DECLINE (sound: a declined term forgoes a proof).
	setDecls    map[string]string // set SMT symbol -> key/element SMT sort
	dictDecls   map[string]string // dict SMT base symbol -> key SMT sort
	lenDecls    map[string]bool   // Elisa ident -> declared length Int (its `.count`/`.len`), asserted >= 0
	lenConsts   map[string]int64
	nonNegDecls map[string]bool // SMT Int consts known non-negative by type (e.g. unsigned field projections)
	// unsignedBits / signedBits record the bit-width of each free var known to be unsigned / signed, so
	// factPreamble can assert the true type bound (`[0, 2^w)` unsigned, `[-2^(w-1), 2^(w-1))` signed).
	// This is what makes the wraparound model (wrapMachineArith) PRECISE rather than merely sound: with
	// operands pinned to their representable range an in-range computation does not actually wrap, so
	// divisibility/alignment proofs through `value - value%alignment` survive.
	unsignedBits     map[string]int
	signedBits       map[string]int
	boolDecls        map[string]bool     // free bool-typed idents declared as SMT Bool consts
	boolLocalRefs    map[string]*Symbol  // source bool locals translated from the current proof scope
	boolDefs         map[string]string   // query-demanded immutable bool locals defined as SMT macros
	boolDefOrder     []string            // dependency-first order for boolDefs in the fact preamble
	onBoolIdentifier func(string)        // optional observer for newly translated free bool identifiers
	resultFields     map[string]ast.Expr // when `result` is a struct literal, its field -> arg expr
	paramConsts      map[string]int64    // law static params bound to constants
	// auxDecls holds pre-formatted declare/assert lines for fresh under-constrained symbols minted
	// for sub-terms we cannot model precisely yet soundly (e.g. `x % y` with a not-provably-nonzero
	// divisor). Each is a free integer constrained only by what is provably true (never a false
	// relation), so the term stays PRESENT — letting the rest of the clause (a guarding `or`, an
	// outer comparison) still discharge — without the abstraction ever fabricating a proof.
	auxDecls []string // declaration + sound-constraint lines, in mint order
	auxVars  []string // the fresh symbols, for the Sat counterexample query
	auxSeq   int      // monotonic counter → deterministic fresh names
	// ceExprs are diagnostic-only SMT terms worth reading back from a Sat model. Quantified forall
	// failures use these to name the binder and array element/select that falsified the goal.
	ceExprs map[string]string // rendered label -> SMT term
	// oldAsEntry enables `old(expr)` -> the ENTRY-value term (vcOldEntry) in the VC IR. Set ONLY by the
	// WP discharge paths, which thread the bare (exit) value so entry/exit diverge; everywhere else
	// old() must stay a fresh opaque symbol or it would collapse onto the un-threaded bare value.
	oldAsEntry bool
	// callCanon canonicalizes a PURE, TOTAL function call to a single SMT symbol keyed by callee +
	// syntactic args, so two occurrences of `g(n-1)` share one symbol (a function is deterministic, so
	// equal inputs give equal outputs — sound). It is what lets the recursive-function defining
	// equation, and an inductive hypothesis about `g(n-1)`, actually CONNECT to the obligation's
	// `g(n)`/`g(n-1)` terms instead of being unrelated fresh aux symbols.
	callCanon map[string]string
	// callProjectionCount numbers aggregate call projection tokens (analyzer_smt_call_projection.go).
	callProjectionCount int
	// callProjectionTypes records each projection token's aggregate result type, so a field read
	// through a token (`result.valid` in a callee's ensure) is typed without the callee's clause nodes.
	callProjectionTypes map[string]Type
	// generalizeGoalAtoms makes lowerVCFormula replace an untranslatable goal atom by a fresh Bool.
	generalizeGoalAtoms bool
	// ghostUFDecls names the uninterpreted functions already declared for ghost-call congruence.
	ghostUFDecls map[string]bool
	// scalarCallUFNames assigns collision-free names to state-independent scalar call models.
	scalarCallUFNames map[string]string
	// Container theory (analyzer_smt_container_theory.go): bases whose value is fixed for the query,
	// and the callee whose formals are being bound.
	stableContainers map[string]bool
	// loopFrameContainers names arrays a loop-preservation query reads at the loop head: the
	// query's pre-state is one free snapshot, and the captured body writes them only through
	// recordOverlays, which apply while recordOverlayActive (the post-body obligation).
	loopFrameContainers map[string]bool
	// arrayElementRanges holds each integer-element array's element value range [lo, hi).
	arrayElementRanges map[string][2]string
	// opaqueQuantifierBodies numbers the fresh predicates opaqueGuardedQuantifierBody declares.
	opaqueQuantifierBodies int
	recordOverlays         map[string]loopArrayStore
	recordOverlayActive    bool
	// stableRecordRoots holds the datatype tokens a callee formal is bound to when the caller passed
	// a stable record root; the callee's container fields of that formal are the caller's.
	stableRecordRoots map[string]bool
	bindingCallee     *ast.FuncDecl
	// callEqInFlight guards recursive instantiation by CANONICAL CALL KEY, not just callee. `g(n)` and
	// `g(n-1)` must remain distinct; only re-entering the exact same ground instance consumes fuel.
	callEqInFlight map[string]int
	// callEqMaxUnroll bounds how deep the defining equation is instantiated (default 1). A finite depth
	// keeps the emitted axiom set finite; soundness does not depend on the depth (only on purity +
	// termination), so this is purely a completeness knob.
	callEqMaxUnroll int
	// callEqDeclDepth bounds defining-equation unfolding PER CALLEE declaration. callEqInFlight keys on
	// the canonical call (decl+args), so genuine recursion f(n)->f(n-1)->f(n-2) mints a fresh key each
	// level and the per-key bound never trips — an infinite unfold. This per-decl counter trips after
	// callEqMaxDeclDepth nested instantiations of the SAME function, leaving the deeper self-call opaque
	// (still sound: a missing equation only weakens the hypothesis set; the recursive IH already carries
	// the inductive content).
	callEqDeclDepth map[*ast.FuncDecl]int
	calleeBodyDepth int
	calleeBodyDecl  *ast.FuncDecl
	// callEqMaxDeclDepth bounds callEqDeclDepth.
	callEqMaxDeclDepth int
	// callClauseDepth bounds nested requires/ensures translation. Unlike defining
	// equations, contract clauses can call functions with distinct argument keys, so
	// callEqInFlight and callEqDeclDepth do not stop recursive clause expansion.
	// At the limit the integer and Boolean call translators return an unconstrained
	// symbol and omit deeper clauses, which only weakens the SMT assumptions.
	callClauseDepth int
	// callClauseExpansions caps new direct-call contract expansions for one
	// obligation. A depth cap alone still permits a broad contract tree to expand
	// exponentially through distinct argument keys.
	callClauseExpansions int
	// callClauseInFlight prevents recursive and mutually-recursive contracts from
	// expanding their own clauses again at each new argument tuple.
	callClauseInFlight map[*ast.FuncDecl]bool
	// callKeyOwnerIndexes cache exact AST ownership and top-level immutable-local
	// declarations for recursive call-key normalization. The source AST is immutable
	// during translation, so each owner body only needs one walk per SMT obligation.
	callKeyOwnerIndexes map[*ast.FuncDecl]*smtCallKeyOwnerIndex
	// requiresHypMode is set ONLY while emitting an entry `requires` clause as a hypothesis. In that mode
	// an unsigned upper-bound-by-type-max comparison (`a + b <= 2^W-1`) is read as the ℤ no-overflow
	// predicate rather than B2's wrapped bvule (which is vacuously true, since `int2bv` reduces the sum
	// mod 2^W). This makes such a precondition a real constraint — paired with the body's matching `+`
	// being emitted clean only WHEN this same requires proves it no-wrap (provablyNoArithWrap →
	// knownRequiresSumNoWrap). Without the clause, neither the hypothesis nor the clean body term exists,
	// so the genuine overflow counterexample is preserved (the C1 soundness gate).
	requiresHypMode bool
	// enumDecls holds enum tag/payload symbol declarations (with their range facts), keyed by symbol.
	enumDecls map[string]string
	// enumBindings maps an env token to the enum-valued expression (and its env) it stands for, so a
	// substituted constructor keeps its constant tag and payload arguments.
	enumBindings map[string]smtEnumBinding
	// enumPinnedBinders is set only while rendering one `is` path fact (see enumIsFactTerm).
	enumPinnedBinders map[string]*Symbol
	// sviewArgCount numbers the fresh length-only sview bindings (see bindSMTSViewLengthArg).
	sviewArgCount          int
	sviewEqualities        map[smtSViewEqualityKey]string
	sviewSliceArrays       map[smtSViewSliceKey]string
	runtimeQuantifierCount int
	// lengthOnly marks env tokens that carry a container length but no element array
	// (see bindSMTLengthOnlyArg); array-term lookups that hit one decline.
	lengthOnly map[string]bool
}

const smtCallClauseMaxDepth = 32
const smtCallClauseMaxExpansions = 4096

// newSMTTranslator builds a translator with all collection maps initialized.
func (a *Analyzer) newSMTTranslator(paramConsts map[string]int64) *smtTranslator {
	if paramConsts == nil {
		paramConsts = map[string]int64{}
	}
	return &smtTranslator{
		a:                   a,
		stringParamInts:     map[smtStringParameterKey]string{},
		stringLiteralInts:   map[string]int64{},
		decls:               map[string]bool{},
		arrayDecls:          map[string]bool{},
		setDecls:            map[string]string{},
		dictDecls:           map[string]string{},
		lenDecls:            map[string]bool{},
		lenConsts:           map[string]int64{},
		nonNegDecls:         map[string]bool{},
		unsignedBits:        map[string]int{},
		signedBits:          map[string]int{},
		boolDecls:           map[string]bool{},
		boolLocalRefs:       map[string]*Symbol{},
		boolDefs:            map[string]string{},
		paramConsts:         paramConsts,
		ceExprs:             map[string]string{},
		callCanon:           map[string]string{},
		callClauseInFlight:  map[*ast.FuncDecl]bool{},
		callKeyOwnerIndexes: map[*ast.FuncDecl]*smtCallKeyOwnerIndex{},
		callEqInFlight:      map[string]int{},
		callEqMaxUnroll:     1,
		callEqDeclDepth:     map[*ast.FuncDecl]int{},
		callEqMaxDeclDepth:  2,
		enumDecls:           map[string]string{},
		enumBindings:        map[string]smtEnumBinding{},
	}
}
