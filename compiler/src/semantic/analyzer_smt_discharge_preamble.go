package semantic

import (
	"os"
	"sort"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// recursiveProofMembers returns the functions on a call cycle through the current function. The
// set is memoized while its resolution inputs are unchanged; a lookup-recording analysis always
// recomputes so every resolver read is still recorded.
func (a *Analyzer) recursiveProofMembers() map[*ast.FuncDecl]bool {
	compute := func() map[*ast.FuncDecl]bool {
		members := map[*ast.FuncDecl]bool{}
		for _, edge := range a.collectRecursiveSCCEdges(a.currentFuncDecl) {
			members[edge.Caller] = true
			members[edge.Callee] = true
		}
		return members
	}
	if a.lookupTrace != nil {
		return compute()
	}
	key := a.resolutionContextKey(a.currentFuncDecl)
	if members, ok := a.recursiveProofMemo[key]; ok {
		return members
	}
	members := compute()
	if a.recursiveProofMemo == nil {
		a.recursiveProofMemo = map[recursiveProofKey]map[*ast.FuncDecl]bool{}
	}
	a.recursiveProofMemo[key] = members
	return members
}

func (a *Analyzer) recursiveCallRequiresProven(callee *ast.FuncDecl, call *ast.CallExpr) bool {
	if a == nil || callee == nil || call == nil {
		return false
	}
	if len(callee.Requires) == 0 {
		return true
	}
	subst := map[string]ast.Expr{}
	args := proofCallArgs(call)
	for i, param := range callee.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	callPath := a.contractCallPath(call)
	for _, req := range callee.Requires {
		if req == nil {
			continue
		}
		switch a.proveRequiresClause(req, subst) {
		case requiresProven:
			continue
		case requiresRefuted:
			return false
		default:
			proven, _ := a.trySMTProveRequiresWithPath(req, subst, callPath, callee.Params)
			if !proven {
				return false
			}
		}
	}
	return true
}

// callEquationEligible reports whether a function's defining equation may be soundly assumed:
//   - PURE: a single `return <pure-expr>` body (after leading contracts) over integer params, with an
//     integer return. Such a body has no mutation, loops, or effects, so `f(args)` equals the body
//     evaluated at the arguments — a true mathematical equation.
//   - TOTAL: if the function makes ANY direct self-recursive call it MUST carry a VERIFIED `decreases`
//     measure. A non-terminating recursive function does not denote a value, and assuming its
//     "equation" can be inconsistent (`f(n) == 1 + f(n)`), which would prove anything. The termination
//     gate makes the recursion well-founded, so the equation has a unique fixed point and is sound.
//
// A non-recursive pure function needs no measure (it is trivially total). Eligibility is cached via the
// analyzer's defining-equation cache to avoid re-deriving purity per call.
func (tr *smtTranslator) callEquationEligible(decl *ast.FuncDecl) bool {
	return tr.a.functionDefiningEquationEligible(decl)
}

// factPreamble emits the declarations for every free variable the translation touched, plus the
// integer flow facts known about them (range bounds, written-constant equalities) as hypotheses. The
// facts are a SOUND SUBSET of what holds, which is why only an `unsat` result concludes a proof.
// takeFactPreamble returns the preamble a caller already rendered for this exact translator state
// (see smtCheckVCMulti), else renders it. The handoff is single-use.
func (tr *smtTranslator) takeFactPreamble() string {
	if !tr.reusePreambleOK {
		return tr.factPreamble()
	}
	preamble := tr.reusePreamble
	tr.reusePreamble, tr.reusePreambleOK = "", false
	if smtPreambleShadow {
		if fresh := tr.factPreamble(); fresh != preamble {
			panic("smt preamble reuse: stale preamble")
		}
	}
	return preamble
}

var smtPreambleShadow = os.Getenv("ELISA_SMT_PREAMBLE_SHADOW") != ""

func (tr *smtTranslator) factPreamble() string {
	names := make([]string, 0, len(tr.decls))
	for name := range tr.decls {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic query text (stable across runs / cache-friendly)
	b := tr.a.borrowSMTText()
	defer tr.a.releaseSMTText(b)
	if tr.a != nil && tr.a.currentClosedWorldTheory != "" {
		b.WriteString("; closed-world theory: " + tr.a.currentClosedWorldTheory + "\n")
	}
	for _, name := range names {
		b.WriteString("(declare-const " + smtVar(name) + " Int)\n")
	}
	rangeFacts := func() {
		for _, name := range names {
			v := smtVar(name)
			if r, ok := tr.a.lookupRangeFact(name); ok {
				b.WriteString(strings.Join(tr.rangeBoundHypotheses(name, r), ""))
			}
			if c, ok := tr.a.writtenConstInt(name); ok {
				if term, ok := tr.varConstTerm(name, c); ok {
					b.WriteString("(assert (= " + v + " " + term + "))\n")
				}
			}
			if tr.nonNegDecls[name] {
				b.WriteString("(assert (>= " + v + " 0))\n")
			}
			if bits, ok := tr.unsignedBits[name]; ok && bits > 0 && bits <= 64 {
				// True type bound: an unsigned value lives in [0, 2^width). Pinning the upper bound makes
				// the wraparound model exact for in-range computations (so divisibility through
				// `value - value%alignment` proves) and still sound (it can only remove counterexamples).
				b.WriteString("(assert (< " + v + " " + smtPow2(bits) + "))\n")
			}
			if bits, ok := tr.signedBits[name]; ok && bits > 0 && bits <= 64 {
				// True type bound: a signed value lives in [-2^(width-1), 2^(width-1)).
				bias := smtPow2(bits - 1)
				b.WriteString("(assert (>= " + v + " (- " + bias + ")))\n")
				b.WriteString("(assert (< " + v + " " + bias + "))\n")
			}
		}
	}
	if tr.a != nil {
		tr.a.withPinnedRelationalEdges(rangeFacts)
	} else {
		rangeFacts()
	}
	// Array declarations (docs/90 brick 90-5): each integer-element array/darray modeled as an SMT
	// (Array Int Int). Deterministic order.
	arrays := make([]string, 0, len(tr.arrayDecls))
	for name := range tr.arrayDecls {
		arrays = append(arrays, name)
	}
	sort.Strings(arrays)
	for _, name := range arrays {
		b.WriteString("(declare-const " + smtVar(name) + " (Array Int Int))\n")
	}
	for _, name := range arrays {
		arr := smtVar(name)
		if r, ok := tr.arrayElementRanges[arr]; ok {
			sel := "(select " + arr + " k)"
			b.WriteString("(assert (forall ((k Int)) (! (and (<= " + r[0] + " " + sel + ") (< " + sel + " " + r[1] + ")) :pattern (" + sel + "))))\n")
		}
	}
	// Set declarations: an `(Array <KSort> Bool)` membership function. Deterministic order.
	sets := make([]string, 0, len(tr.setDecls))
	for sym := range tr.setDecls {
		sets = append(sets, sym)
	}
	sort.Strings(sets)
	for _, sym := range sets {
		b.WriteString("(declare-const " + sym + " (Array " + tr.setDecls[sym] + " Bool))\n")
	}
	// Dict declarations: a `<sym>_keys` membership array plus a `<sym>_vals` value array, sharing the
	// key sort. The value array is fixed to Int (only integer-valued dicts are modeled). Deterministic.
	dicts := make([]string, 0, len(tr.dictDecls))
	for sym := range tr.dictDecls {
		dicts = append(dicts, sym)
	}
	sort.Strings(dicts)
	for _, sym := range dicts {
		ks := tr.dictDecls[sym]
		b.WriteString("(declare-const " + sym + "_keys (Array " + ks + " Bool))\n")
		b.WriteString("(declare-const " + sym + "_vals (Array " + ks + " Int))\n")
	}
	// Free bool consts (bool params/locals appearing in a boolean postcondition). Deterministic order.
	bools := make([]string, 0, len(tr.boolDecls))
	for name := range tr.boolDecls {
		bools = append(bools, name)
	}
	sort.Strings(bools)
	for _, name := range bools {
		if _, defined := tr.boolDefs[name]; defined {
			continue
		}
		b.WriteString("(declare-const " + name + " Bool)\n")
	}
	// Length symbols (`arr.count`/`.len`), each a non-negative Int.
	lens := make([]string, 0, len(tr.lenDecls))
	for sym := range tr.lenDecls {
		lens = append(lens, sym)
	}
	sort.Strings(lens)
	for _, sym := range lens {
		b.WriteString("(declare-const " + sym + " Int)\n")
		b.WriteString("(assert (>= " + sym + " 0))\n")
		// A container length is a `usize`, so it is bounded above by 2^64 by construction. Asserting this
		// (always-true) upper bound lets an UNSIGNED loop guard `i < arr.count` — lowered as a bitvector
		// `bvult` over `(int2bv 64 …)` — bridge back to Int reasoning: without `arr.count < 2^64` the
		// `int2bv` of an unbounded length could wrap, so z3 cannot conclude the Int relation the
		// termination/preservation goal needs. Sound: it only ever asserts a fact the type guarantees.
		if strings.HasPrefix(sym, "|sview_length_") {
			// A string view's `.len` is an `i64`, not a `usize`: it can never exceed i64.max. The
			// looser usize bound let a measure such as `v.len - i` (i64 arithmetic, modeled with
			// two's-complement wrap) appear to wrap negative for an unreachable length >= 2^63,
			// so `decreases v.len - i` over an immutable view could not be proven.
			b.WriteString("(assert (< " + sym + " 9223372036854775808))\n")
			continue
		}
		b.WriteString("(assert (< " + sym + " 18446744073709551616))\n")
	}
	// Fresh under-constrained symbols (modulo/div with a not-provably-nonzero divisor). Emitted in
	// mint order — deterministic because the counter is monotonic over a single translation.
	var auxAssertions []string
	// Enum metadata also carries view-length equations. A sliced view's length can refer to an
	// auxiliary call result, so those equations must wait for all declarations and Bool macros.
	for line := range strings.SplitSeq(tr.enumPreamble(), "\n") {
		if strings.HasPrefix(line, "(assert ") {
			if smtTermMentionsQuantifierBinder(line) {
				continue
			}
			auxAssertions = append(auxAssertions, line+"\n")
		} else if line != "" {
			b.WriteString(line + "\n")
		}
	}
	for _, line := range tr.auxDecls {
		// An auxiliary ASSUMPTION minted while translating a quantifier body (a callee's defining
		// equation or ensure over the binder) is hoisted to top level, where the `q_` binder is
		// unbound and z3 would reject the whole query. Dropping an assumption only weakens it.
		if strings.HasPrefix(line, "(assert ") && smtTermMentionsQuantifierBinder(line) {
			continue
		}
		if strings.HasPrefix(line, "(assert ") {
			auxAssertions = append(auxAssertions, line)
			continue
		}
		b.WriteString(line)
	}
	// Immutable local bools are macros rather than free constants plus equalities. Emit them after
	// every declaration (including auxiliary symbols minted while translating an initializer), in
	// dependency-first order so SMT-LIB never needs a forward reference.
	for _, name := range tr.boolDefOrder {
		value, ok := tr.boolDefs[name]
		if !ok {
			continue
		}
		b.WriteString("(define-fun " + name + " () Bool " + value + ")\n")
	}
	// Auxiliary constraints can mention a Boolean local that became a macro after its translation.
	// Defer those assertions until the macros are defined, while keeping all auxiliary declarations
	// above them so both the assertions and macro bodies have every symbol in scope.
	for _, line := range auxAssertions {
		b.WriteString(line)
	}
	return b.String()
}

// declaredSMTVars returns the SMT symbols for every free variable the translation declared, so the
// solver can be asked for their values on a Sat (counterexample) result.
func (tr *smtTranslator) declaredSMTVars() []string {
	out := make([]string, 0, len(tr.decls))
	for name := range tr.decls {
		out = append(out, smtVar(name))
	}
	sort.Strings(out)
	out = append(out, tr.auxVars...)
	labels := make([]string, 0, len(tr.ceExprs))
	for label := range tr.ceExprs {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		out = append(out, smtCounterexampleExprVar(label))
	}
	return out
}

func (tr *smtTranslator) counterexampleExprDefinitions() string {
	if len(tr.ceExprs) == 0 {
		return ""
	}
	labels := make([]string, 0, len(tr.ceExprs))
	for label := range tr.ceExprs {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	var b strings.Builder
	for _, label := range labels {
		b.WriteString("(define-fun ")
		b.WriteString(smtCounterexampleExprVar(label))
		b.WriteString(" () Int ")
		b.WriteString(tr.ceExprs[label])
		b.WriteString(")\n")
	}
	return b.String()
}

// counterexample renders a model (SMT-var → value) as a readable "a=5, b=20" hint using the original
// Elisa identifier names. Empty when no model is available.
func (tr *smtTranslator) counterexample(model map[string]string) string {
	if len(model) == 0 {
		return ""
	}
	names := make([]string, 0, len(tr.decls))
	for name := range tr.decls {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if v, ok := model[smtVar(name)]; ok {
			parts = append(parts, name+"="+v)
		}
	}
	labels := make([]string, 0, len(tr.ceExprs))
	for label := range tr.ceExprs {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		if v, ok := model[smtCounterexampleExprVar(label)]; ok {
			parts = append(parts, label+"="+v)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func smtCounterexampleExprVar(label string) string {
	var b strings.Builder
	b.WriteString("ce_")
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func smtCompare(op lexer.TokenKind, l, r string) string {
	if folded, ok := smtFoldCompare(op, l, r); ok {
		return folded
	}
	switch op {
	case lexer.TOKEN_GT:
		return "(> " + l + " " + r + ")"
	case lexer.TOKEN_GTEQ:
		return "(>= " + l + " " + r + ")"
	case lexer.TOKEN_LT:
		return "(< " + l + " " + r + ")"
	case lexer.TOKEN_LTEQ:
		return "(<= " + l + " " + r + ")"
	case lexer.TOKEN_EQEQ:
		return "(= " + l + " " + r + ")"
	case lexer.TOKEN_BANGEQ:
		return "(distinct " + l + " " + r + ")"
	}
	return "false"
}

// smtInt renders an integer literal, parenthesizing negatives as SMT-LIB requires `(- n)`.
func smtInt(v int64) string {
	if v < 0 {
		return "(- " + strconv.FormatInt(-v, 10) + ")"
	}
	return strconv.FormatInt(v, 10)
}

// smtAbs renders integer absolute value. The caller is responsible for keeping terms in the integer
// fragment; SMT-LIB Int arithmetic is total.
func smtAbs(term string) string {
	return "(ite (< " + term + " 0) (- " + term + ") " + term + ")"
}

// smtTruncDiv renders Elisa/C-style integer division, which truncates toward zero. SMT-LIB `div` is
// Euclidean, so for negative operands we divide absolute values and restore the quotient sign.
func smtTruncDiv(left, right string) string {
	if l, r, ok := smtIntPair(left, right); ok && r.Sign() != 0 {
		// big.Int.Quo truncates toward zero, exactly the modeled semantics.
		return smtBigInt(l.Quo(l, r))
	}
	absLeft := smtAbs(left)
	absRight := smtAbs(right)
	quot := "(div " + absLeft + " " + absRight + ")"
	sameSign := "(= (< " + left + " 0) (< " + right + " 0))"
	return "(ite " + sameSign + " " + quot + " (- " + quot + "))"
}

// smtVar maps an Elisa identifier to a collision-free SMT symbol.
func smtVar(name string) string {
	return "v_" + name
}

// smtBoolVar names an SMT Bool const for a free bool identifier — a distinct namespace from the
// integer `v_` consts so an int and a bool of the same Elisa name never collide.
func smtBoolVar(name string) string {
	return "b_" + name
}

// tupleLiteralFieldValue projects one named field without allocating a map.
// An ambiguous or untyped projection is left to the existing solver decline.
func (a *Analyzer) tupleLiteralFieldValue(tuple *ast.TupleExpr, name string) (ast.Expr, bool) {
	if tuple == nil || name == "" {
		return nil, false
	}
	typ, ok := contextualTupleType(a.exprType(tuple))
	if !ok || len(typ.Fields) != len(tuple.Elems) {
		return nil, false
	}
	var value ast.Expr
	for i, field := range typ.Fields {
		if field.Name != name {
			continue
		}
		if value != nil || tuple.Elems[i] == nil || !a.literalComponentReevaluableAt(tuple.Elems, i) {
			return nil, false
		}
		value = tuple.Elems[i]
	}
	return value, value != nil
}

// tupleLiteralFieldMap resolves only fields of an already typed tuple literal.
// Contextual analysis records named return fields; never guess names from the
// current function, which could belong to a different tuple expression.
func (a *Analyzer) tupleLiteralFieldMap(tuple *ast.TupleExpr) map[string]ast.Expr {
	if tuple == nil {
		return nil
	}
	typ, ok := contextualTupleType(a.exprType(tuple))
	if !ok || len(typ.Fields) != len(tuple.Elems) {
		return nil
	}
	reevaluable := a.literalComponentReevaluable(tuple.Elems)
	fields := make(map[string]ast.Expr, len(typ.Fields))
	for i, field := range typ.Fields {
		if field.Name == "" || tuple.Elems[i] == nil || !reevaluable[i] {
			continue
		}
		if _, duplicate := fields[field.Name]; duplicate {
			return nil
		}
		fields[field.Name] = tuple.Elems[i]
	}
	return fields
}

// structLitFieldMap maps each field name of a struct literal to its construction argument, handling
// both named args and positional args (resolved against the struct declaration's field order). Returns
// nil if the field order cannot be resolved.
func (a *Analyzer) structLitFieldMap(sl *ast.StructLitExpr) map[string]ast.Expr {
	if sl == nil {
		return nil
	}
	if sl.ResolvedArgsValid {
		typ, ok := stripRefForBounds(a.exprType(sl)).(*StructType)
		if !ok || typ == nil || typ.Decl == nil || len(sl.ResolvedArgs) != len(typ.Decl.Fields) {
			return nil
		}
		out := make(map[string]ast.Expr, len(typ.Decl.Fields))
		reevaluable := a.structLiteralReevaluable(sl)
		for index, field := range typ.Decl.Fields {
			if field.Name == "" || sl.ResolvedArgs[index] == nil {
				return nil
			}
			if _, duplicate := out[field.Name]; duplicate {
				return nil
			}
			if a.constructorProjectionStable(sl.ResolvedArgs[index]) || reevaluable[sl.ResolvedArgs[index]] {
				out[field.Name] = sl.ResolvedArgs[index]
			}
		}
		return out
	}
	if sl.CopyBase != nil || len(sl.Spreads) != 0 {
		return nil
	}
	reevaluable := a.structLiteralReevaluable(sl)
	args := sl.Args
	var ordered []string
	if st, ok := stripRefForBounds(a.exprType(sl)).(*StructType); ok && st != nil && st.Decl != nil {
		for _, fd := range st.Decl.Fields {
			ordered = append(ordered, fd.Name)
		}
	}
	out := map[string]ast.Expr{}
	for i, arg := range args {
		name := sl.ArgName(i)
		if name == "" {
			if i >= len(ordered) {
				continue
			}
			name = ordered[i]
		}
		if name != "" && arg != nil && (a.constructorProjectionStable(arg) || reevaluable[arg]) {
			out[name] = arg
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
