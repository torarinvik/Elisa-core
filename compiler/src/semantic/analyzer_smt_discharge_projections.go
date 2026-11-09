package semantic

import (
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// zeroedStructLocalFieldMap builds a field→expr map for a `= zeroed` struct local whose root has not
// been wholesale-replaced since declaration (tracked by a.zeroedStructLocals). It is the zeroed-local
// analogue of structLitFieldMap: rather than reading construction arguments, it reads the last value
// written to each field from the live writtenField scope chain, and falls back to the integer literal
// `0` for fields that were never written (sound because `zeroed` zero-initializes every field and the
// root has not been replaced). Returns nil when the local is unknown, the type is not a resolved struct,
// or no struct type declaration is available (so the field set cannot be enumerated).
//
// Soundness argument:
//   - a.zeroedStructLocals[name] is set at `name: T = zeroed` and cleared in
//     invalidateWrittenFieldsForRoot(name) — which fires on whole-root reassignment, mutable borrow,
//     or escape to a mutating callee. So if the entry is present, every field not in writtenField has
//     never been touched, hence still holds the zeroed initial value.
//   - writtenField entries for `name.f` are dropped by invalidateWrittenField or
//     invalidateWrittenFieldsForRoot at every mutation/escape of root or field, so a stale fact can
//     never persist.
//   - We only use isStableWrittenFieldValue-passing values (recorded by recordWrittenFieldForTarget),
//     so the stored expressions are either constants or pure ident/field projections — re-evaluation
//     is safe and cannot introduce side-effects.
func (a *Analyzer) zeroedStructLocalFieldMap(id *ast.Ident) map[string]ast.Expr {
	if id == nil || !a.zeroedStructLocals[id.Name] {
		return nil
	}
	sym, ok := a.currentScope.Lookup(id.Name)
	if !ok || sym == nil {
		return nil
	}
	st, ok := stripRefForBounds(sym.Type).(*StructType)
	if !ok || st == nil || st.Decl == nil {
		return nil
	}
	zeroLit := &ast.IntLit{Value: "0"}
	out := make(map[string]ast.Expr, len(st.Decl.Fields))
	for _, fd := range st.Decl.Fields {
		key := id.Name + smtFieldSeparator + fd.Name
		if v, found := a.lookupWrittenField(key); found {
			out[fd.Name] = v
		} else {
			// Field was never written after zeroed initialization — still holds 0.
			out[fd.Name] = zeroLit
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// nullComparePointer returns the non-null operand of a `p == null` / `p != null` comparison, or nil if
// neither operand is the null literal.
func nullComparePointer(n *ast.BinaryExpr) ast.Expr {
	if _, ok := stripOptimizationParens(n.Right).(*ast.NullLit); ok {
		return n.Left
	}
	if _, ok := stripOptimizationParens(n.Left).(*ast.NullLit); ok {
		return n.Right
	}
	return nil
}

// smtFieldSeparator cannot occur in an Elisa identifier (including a field name),
// and is legal in a simple SMT symbol. Thus root and field components round-trip
// without escaping or allocating encoded copies. Generated roots start with @,
// which also cannot occur in source identifiers.
const smtFieldSeparator = "."

func smtProjectionName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		return smtProjectionName(n.Object) + smtFieldSeparator + n.Field
	case *ast.ParenExpr:
		return smtProjectionName(n.Inner)
	default:
		return "@expr." + fmt.Sprintf("%x", fmt.Sprintf("%T_%s", expr, expr.Pos().String()))
	}
}

func sortedStructFieldNames(st *StructType) []string {
	if st == nil {
		return nil
	}
	names := make([]string, 0, len(st.Fields))
	for name := range st.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func smtSanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Callee clauses cannot capture ambient caller bindings by spelling.
func (tr *smtTranslator) calleeBoolTerm(decl *ast.FuncDecl, expr ast.Expr, env map[string]string) (string, bool) {
	priorDecl := tr.calleeBodyDecl
	tr.calleeBodyDecl = decl
	tr.calleeBodyDepth++
	defer func() { tr.calleeBodyDecl = priorDecl; tr.calleeBodyDepth-- }()
	return tr.boolTerm(expr, env)
}

// smtQueryTrace dumps solver queries to stderr for prover debugging: ELISA_SMT_TRACE=1 traces
// every function, any other value traces only the function of that name.
var smtQueryTrace = os.Getenv("ELISA_SMT_TRACE")

func (a *Analyzer) smtTraceOn() bool {
	return smtQueryTrace == "1" || (smtQueryTrace != "" && a.currentFuncName() == smtQueryTrace)
}

func (a *Analyzer) currentFuncName() string {
	if a.currentFuncDecl == nil {
		return "<none>"
	}
	return a.currentFuncDecl.Name
}

// calleeFreeProjection reports whether, while lowering a CALLEE's body or contract (calleeBodyDepth >
// 0), `expr` is a place rooted at an identifier the call environment does not bind. Such a root is the
// callee's own formal or local; resolving it through a name-keyed symbol (`v_l.a`,
// `isnull_p`, an array or length symbol) or the caller's scope would silently alias an unrelated
// caller variable of the same spelling — a false proof. Every projection fallback declines on it.
func (tr *smtTranslator) calleeFreeProjection(expr ast.Expr, env map[string]string) bool {
	if tr == nil || tr.calleeBodyDepth == 0 {
		return false
	}
	root := stripOptimizationParens(expr)
	for {
		switch n := root.(type) {
		case *ast.FieldExpr:
			if env != nil {
				if _, ok := env[smtProjectionName(n)]; ok {
					return false
				}
			}
			root = stripOptimizationParens(n.Object)
			continue
		case *ast.IndexExpr:
			root = stripOptimizationParens(n.Object)
			continue
		case *ast.Ident:
			if env != nil {
				if _, ok := env[n.Name]; ok {
					return false
				}
			}
			// A module path / type / constant root is global, not a callee local.
			if _, ok := tr.a.constIntValue(n); ok {
				return false
			}
			if _, _, ok := tr.a.lookupVisibleType(n.Name); ok {
				return false
			}
			return true
		default:
			return false
		}
	}
}

// constIntTerm renders a folded integer constant at its mathematical value. The constant folder
// stores values as int64 bit patterns, so an unsigned constant at or above 2^63 (a u64 FNV offset)
// arrives negative; emitting it as-is contradicts the unsigned range facts and lets any obligation
// "prove" vacuously. Reinterpret negative values of an unsigned-typed expression modulo 2^width.
func (tr *smtTranslator) constIntTerm(expr ast.Expr, c int64) string {
	if c >= 0 || tr == nil || tr.a == nil {
		return smtInt(c)
	}
	t := tr.a.exprType(expr)
	if t == nil {
		if id, ok := expr.(*ast.Ident); ok {
			if name, resolved := tr.a.resolvedValueNames[id]; resolved && tr.a.globalScope != nil {
				if sym, found := tr.a.globalScope.Lookup(name); found && sym != nil {
					t = sym.Type
				}
			} else if tr.a.currentScope != nil {
				if sym, found := tr.a.currentScope.Lookup(id.Name); found && sym != nil {
					t = sym.Type
				}
			}
		}
	}
	signed, bits, ok := smtIntWidthSign(t)
	if !ok || signed {
		return smtInt(c)
	}
	v := new(big.Int).SetInt64(c)
	v.Add(v, new(big.Int).Lsh(big.NewInt(1), uint(bits)))
	return v.String()
}

// unsignedVarBits reports the width of an in-scope unsigned integer variable.
func (tr *smtTranslator) unsignedVarBits(name string) (int, bool) {
	if tr == nil || tr.a == nil || tr.a.currentScope == nil {
		return 0, false
	}
	sym, ok := tr.a.currentScope.Lookup(name)
	if !ok || sym == nil {
		return 0, false
	}
	signed, bits, ok := smtIntWidthSign(sym.Type)
	return bits, ok && !signed
}

// varConstTerm renders an int64-folded constant known for variable `name` at its mathematical value.
// The folder keeps int64 bit patterns, so a u64 value >= 2^63 arrives negative; asserting it verbatim
// contradicts the variable's unsigned range and makes every query vacuously provable.
func (tr *smtTranslator) varConstTerm(name string, c int64) (string, bool) {
	if c >= 0 {
		return smtInt(c), true
	}
	bits, unsigned := tr.unsignedVarBits(name)
	if !unsigned {
		return smtInt(c), true
	}
	if bits < 64 {
		return "", false // a negative pattern for a narrower unsigned value is not a value we can trust
	}
	v := new(big.Int).SetInt64(c)
	v.Add(v, new(big.Int).Lsh(big.NewInt(1), uint(bits)))
	return v.String(), true
}

// rangeBoundHypotheses emits a range fact's bounds in the variable's mathematical domain. For an
// unsigned variable an int64 interval with negative ends is a bit-pattern interval: both ends negative
// maps to [lo+2^64, hi+2^64]; an interval straddling zero wraps and only its non-negative part's
// complement is known, so that fact is dropped (sound: fewer hypotheses).
func (tr *smtTranslator) rangeBoundHypotheses(name string, r numRange) []string {
	v := smtVar(name)
	var out []string
	if _, unsigned := tr.unsignedVarBits(name); unsigned && ((r.loKnown && r.lo < 0) || (r.hiKnown && r.hi < 0)) {
		if !(r.loKnown && r.hiKnown && r.lo < 0 && r.hi < 0) {
			return nil
		}
		lo, okLo := tr.varConstTerm(name, r.lo)
		hi, okHi := tr.varConstTerm(name, r.hi)
		if !okLo || !okHi {
			return nil
		}
		return []string{"(assert (>= " + v + " " + lo + "))\n", "(assert (<= " + v + " " + hi + "))\n"}
	}
	if r.loKnown {
		out = append(out, "(assert (>= "+v+" "+smtInt(r.lo)+"))\n")
	}
	if r.hiKnown {
		out = append(out, "(assert (<= "+v+" "+smtInt(r.hi)+"))\n")
	}
	return out
}

// assertGhostCallCongruence ties a ghost call's result symbol to an uninterpreted-function
// application, so calls whose arguments are EQUAL under the hypotheses (not merely spelled alike)
// share a value: `suf(text, i, h)` with `i == 0 and h == OFF` is `suf(text, 0, OFF)`. Canonical
// keys only catch syntactic equality, which is why loop invariants over a ghost fold never proved.
// A `ghost def` is pure and total, so equal arguments give equal results. Integer and Bool
// arguments become UF parameters; a view argument is keyed into the function NAME by its array
// binding (the same symbol is the same view, length included), so two different views never meet
// as one UF argument. Any other argument shape declines (the fresh symbol stays unconstrained).
func (tr *smtTranslator) assertGhostCallCongruence(decl *ast.FuncDecl, args []ast.Expr, argEnv map[string]string, ret string) {
	tr.assertGhostCallCongruenceSort(decl, args, argEnv, ret, "Int")
}

// assertGhostCallCongruenceSort is assertGhostCallCongruence for a result of the given SMT sort.
func (tr *smtTranslator) assertGhostCallCongruenceSort(decl *ast.FuncDecl, args []ast.Expr, argEnv map[string]string, ret, resultSort string) {
	if tr == nil || tr.a == nil || decl == nil || !decl.IsGhost || len(args) != len(decl.Params) {
		return
	}
	env := tr.canonKeyEnv(decl, argEnv)
	// Sorts come from the callee's declared parameter types: an invariant is proven before the loop
	// body (and so before its own argument expressions) is analyzed, so the arguments may be untyped.
	var paramTypes []Type
	if sym, ok := tr.a.symbolForFuncDecl(decl); ok && sym != nil {
		if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil && tr.proofParamCount(fnType) == len(args) {
			paramTypes = fnType.Params
		}
	}
	if paramTypes == nil {
		return
	}
	var name strings.Builder
	name.WriteString(tr.a.smtStableDeclID(decl))
	sorts := make([]string, 0, len(args))
	terms := make([]string, 0, len(args))
	for i, arg := range args {
		if arg == nil {
			return
		}
		argType := stripRefForBounds(paramTypes[i])
		switch {
		case isStringViewType(argType):
			key, ok := tr.ghostViewArgKey(arg, env)
			if !ok {
				return
			}
			name.WriteString("|" + key)
		case IsBoolType(argType):
			t, ok := tr.boolTerm(arg, env)
			if !ok {
				return
			}
			sorts = append(sorts, "Bool")
			terms = append(terms, t)
		case isDArrayTypeMaybeRef(argType):
			if ref, isRef := paramTypes[i].(*RefType); isRef && (ref.Mutable || ref.Linear) {
				return
			}
			inner := stripOptimizationParens(arg)
			if addr, isAddr := inner.(*ast.AddrOfExpr); isAddr && addr != nil {
				inner = addr.Operand
			}
			base, ok := tr.containerBase(inner, env)
			if !ok {
				return
			}
			name.WriteString("|container:" + base)
		default:
			// A read-only record root that the current function never writes is one
			// snapshot for the whole query, so it is keyed into the function name.
			if ref, isRef := paramTypes[i].(*RefType); !isRef || (!ref.Mutable && !ref.Linear) {
				if token, ok := tr.stableRecordRootTokenGate(arg, env, tr.a.sviewFrameWriteFreeGhostReads); ok {
					name.WriteString("|record:" + token)
					continue
				}
			}
			if _, numeric := smtNumericValueType(argType); !numeric {
				return
			}
			t, ok := tr.termEnv(arg, env)
			if !ok {
				return
			}
			sorts = append(sorts, "Int")
			terms = append(terms, t)
		}
	}
	fn := "|ghost_uf_" + strconv.FormatUint(fnvString(name.String()), 16) + "_" + strconv.Itoa(len(sorts)) + "_" + resultSort + "|"
	if tr.ghostUFDecls == nil {
		tr.ghostUFDecls = map[string]bool{}
	}
	if !tr.ghostUFDecls[fn] {
		tr.ghostUFDecls[fn] = true
		tr.auxDecls = append(tr.auxDecls, "(declare-fun "+fn+" ("+strings.Join(sorts, " ")+") "+resultSort+")\n")
	}
	app := fn
	if len(terms) != 0 {
		app = "(" + fn + " " + strings.Join(terms, " ") + ")"
	}
	tr.auxDecls = append(tr.auxDecls, "(assert (= "+ret+" "+app+"))\n")
}

func fnvString(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// ghostViewArgKey names a view argument by the IMMUTABLE binding it reads: the same binding is the
// same view (bytes and length), wherever the call appears in the function. A call-site view is
// otherwise bound to a fresh array per call (bindSMTSViewLengthArg), which shares nothing. A name the
// env binds (a substituted callee formal) or any other expression declines.
func (tr *smtTranslator) ghostViewArgKey(arg ast.Expr, env map[string]string) (string, bool) {
	id, ok := stripOptimizationParens(arg).(*ast.Ident)
	if !ok || id == nil || tr.a.currentScope == nil {
		return "", false
	}
	// The key is the view's byte-array symbol, so it agrees across frames: a callee formal bound
	// to the caller's view carries that view's symbol (never a length-only stand-in).
	if bound, isBound := env[id.Name]; isBound {
		if tr.lengthOnly[bound] || bound == "" {
			return "", false
		}
		return "view:" + bound, true
	}
	if tr.calleeBodyDepth != 0 {
		return "", false
	}
	sym, found := tr.a.currentScope.Lookup(id.Name)
	if !found || sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolLocal) || sym.Mutable || symbolRootMutable(sym) {
		return "", false
	}
	return "view:" + smtVar(id.Name), true
}

// smtCallLocalResultOnlyEnsures asserts, for an immutable numeric local initialized by a direct call
// (`first: i64 = json_hex4(data, cursor.position)`), every callee `ensure` that mentions no formal —
// only `result` and constants (`result == -1 or (result >= 0 and result <= 65535)`). Such a clause
// holds for the value the call returned whatever its arguments were, so it survives later writes to
// the arguments that retire the local's defining equation (`cursor.position <- …`). Gates: the
// callee has no `requires` (its ensures are unconditional), is not the current function or
// recursive (no unverified induction hypothesis), and the local's type is the call's own type (no
// implicit conversion between the returned value and the binding). Formals are left unbound and
// lowered with calleeBoolTerm, so a clause that does read one declines instead of resolving to a
// same-named caller binding.
func (a *Analyzer) smtCallLocalResultOnlyEnsures(tr *smtTranslator, b *strings.Builder, name string, sym *Symbol, value ast.Expr) {
	call, ok := stripOptimizationParens(value).(*ast.CallExpr)
	if !ok || call == nil {
		return
	}
	decl, direct := a.resolveDirectCallFuncDecl(call)
	decl = a.readDecl(decl)
	if !direct || decl == nil || len(decl.Requires) != 0 || len(decl.EnsureValues) == 0 || decl == a.currentFuncDecl || a.isRecursiveProofCall(decl) {
		return
	}
	if a.implicitIntConversion(value, a.exprType(value), sym.Type) != value {
		return
	}
	if _, _, ok := smtIntWidthSign(sym.Type); !ok {
		return
	}
	env := map[string]string{"result": smtVar(name)}
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		h, ok := tr.calleeBoolTerm(decl, ensure, env)
		if !ok {
			continue
		}
		tr.decls[name] = true
		tr.markIntVar(name, sym.Type)
		b.WriteString("(assert " + h + ")\n")
	}
}

// literalMembershipTerm lowers `x in [a, lo ..= hi, …]` (and a bare `x in lo ..< hi`) over an
// integer/char scrutinee and literal candidates the way codegen evaluates it
// (emitMembershipCompareValueAndExpr): a disjunction of `x == a` and `lo <= x <= hi` (`< hi` for
// `..<`). Any other candidate shape declines.
func (tr *smtTranslator) literalMembershipTerm(left, right ast.Expr, env map[string]string) (string, bool) {
	if t, ok := smtNumericValueType(tr.a.exprType(left)); !ok || IsFloatType(t) {
		return "", false
	}
	var elems []ast.Expr
	switch r := stripOptimizationParens(right).(type) {
	case *ast.ListLitExpr:
		if r == nil || r.Keys != nil || r.Owner != nil || len(r.Elems) == 0 {
			return "", false
		}
		for _, spread := range r.Spreads {
			if spread {
				return "", false
			}
		}
		elems = r.Elems
	case *ast.MembershipRangeExpr:
		elems = []ast.Expr{r}
	default:
		return "", false
	}
	literal := func(e ast.Expr) bool {
		switch stripOptimizationParens(e).(type) {
		case *ast.IntLit, *ast.CharLit:
			return true
		}
		return false
	}
	x, ok := tr.termEnv(left, env)
	if !ok {
		return "", false
	}
	var parts []string
	for _, elem := range elems {
		if rng, ok := stripOptimizationParens(elem).(*ast.MembershipRangeExpr); ok {
			if rng == nil || !literal(rng.Start) || !literal(rng.End) {
				return "", false
			}
			lo, ok := tr.termEnv(rng.Start, env)
			if !ok {
				return "", false
			}
			hi, ok := tr.termEnv(rng.End, env)
			if !ok {
				return "", false
			}
			upper := "<="
			if rng.Op == lexer.TOKEN_RANGE_LT {
				upper = "<"
			}
			parts = append(parts, "(and (>= "+x+" "+lo+") ("+upper+" "+x+" "+hi+"))")
			continue
		}
		if !literal(elem) {
			return "", false
		}
		v, ok := tr.termEnv(elem, env)
		if !ok {
			return "", false
		}
		parts = append(parts, "(= "+x+" "+v+")")
	}
	if len(parts) == 1 {
		return parts[0], true
	}
	return "(or " + strings.Join(parts, " ") + ")", true
}

// recordFieldNamed reports whether obj is a tuple or struct value with a declared field `name`.
func (a *Analyzer) recordFieldNamed(obj ast.Expr, name string) bool {
	switch t := stripRefForBounds(a.exprType(obj)).(type) {
	case *TupleType:
		for _, field := range t.Fields {
			if field.Name == name {
				return true
			}
		}
	case *StructType:
		if t != nil && t.Fields != nil {
			_, ok := t.Fields[name]
			return ok
		}
	}
	return false
}
