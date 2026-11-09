package semantic

import (
	"encoding/base64"
	"strings"

	"elisacore/src/ast"
)

// arrayTermEnv lowers an ARRAY-valued expression to an SMT array symbol: an array/darray identifier
// becomes a `(Array Int Int)` const (declared once), or resolves through `env` (the law's `self`).
// Element-typed quantifiers (docs/90 brick 90-5) model `arr[i]` as `(select <arr> i)`.
func (tr *smtTranslator) arrayTermEnv(expr ast.Expr, env map[string]string) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.arrayTermEnv(n.Inner, env)
	case *ast.SliceExpr:
		return tr.sviewSliceArrayTerm(n, env)
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				// A container identity alone does not declare a flat array. An actual
				// array already declared in this query can retain both representations.
				if strings.HasPrefix(bound, dtTokenPrefix) || tr.lengthOnly[bound] || (tr.stableContainers[bound] && !tr.declaredFlatArrayTerm(bound)) {
					return "", false
				}
				return bound, true
			}
		}
		if tr.calleeFreeProjection(n, env) {
			return "", false
		}
		if tr.envBindsViewRoot(n, env) {
			return "", false
		}
		if arr, ok := tr.sviewSliceLocalArrayTerm(n, env); ok {
			return arr, true
		}
		// Resolve the array's element type from exprTypes (set once the node is analyzed) OR, when that
		// is not yet populated — e.g. loop-invariant proving runs BEFORE the body is type-analyzed — from
		// the symbol's declared type via scope lookup. The declared type is authoritative for a param or
		// local, so this is sound and is what lets `arr[k]` in a quantified loop invariant translate.
		t := tr.a.exprType(n)
		if t == nil && tr.a.currentScope != nil {
			if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil {
				t = sym.Type
			}
		}
		if tr.isArrayLike(t) || tr.immutableSviewArrayParameter(n, env) {
			tr.arrayDecls[n.Name] = true
			arr := smtVar(n.Name)
			tr.recordFixedArrayLen(arr, t)
			tr.linkSViewArrayLength(n, arr)
			return arr, true
		}
		return "", false
	case *ast.FieldExpr:
		// An array-valued struct field, possibly through a reference (`r.data`, `self.buf`). Model it
		// as a stable array symbol keyed by its syntactic path, so two reads of the same path share the
		// symbol (and thus its `.count`/`.len`). This is what lets `ensure result <= r.data.count;
		// return r.data.count` discharge — both sides resolve to the same length symbol.
		//
		// Check env FIRST: the loop-preservation prover injects a path-keyed (store …) substitution
		// for struct-field array stores (`s.data[i] <- v` → env["s.data"] = "(store …)").
		// The env key is the same normalizedFieldPath / smtProjectionName, so picking it up here
		// lets `s.data[k]` in a quantified invariant resolve to the post-body array term — which is
		// exactly what the bare-array Ident case does for `arr[k]`.
		if env != nil {
			if bound, ok := env[smtProjectionName(n)]; ok {
				if strings.HasPrefix(bound, dtTokenPrefix) || tr.lengthOnly[bound] {
					return "", false
				}
				return bound, true
			}
		}
		if tr.calleeFreeProjection(n, env) {
			return "", false
		}
		// Resolve the field's array type from exprTypes (set once analyzed) OR — when the node is
		// synthetic/cloned (e.g. the loop-termination prover's measure[body], whose field reads were
		// produced by substitution and never analyzed) — from the object's struct type, mirroring the
		// Ident-case scope fallback above. Both readings resolve the SAME syntactic path to the SAME
		// projection symbol, so a field-array length cancels between measure and measure[body].
		ft := tr.a.exprType(n)
		if ft == nil {
			if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
				ft = resolved
			}
		}
		if tr.isArrayLike(ft) {
			name, ok := tr.fieldProjectionKey(n, env)
			if !ok {
				return "", false
			}
			tr.arrayDecls[name] = true
			arr := smtVar(name)
			tr.recordFixedArrayLen(arr, ft)
			return arr, true
		}
		return "", false
	default:
		return "", false
	}
}

// arrayLenSymForAnyElem returns the SMT length symbol (`<base>_len`) for the `.count`/`.len` of an
// array/darray-valued expression of ANY element type — including a darray of structs, which
// arrayTermEnv (integer-element only) declines. It accepts a bare array identifier or a stable
// struct-field path; the base key is the env-bound symbol or the syntactic projection name, so two
// reads of the same path share one length symbol (the cancellation the decrease proof needs). The
// type is read from exprTypes, falling back to the symbol's declared type (Ident) or the resolved
// field type (FieldExpr) for synthetic/cloned nodes. Declines for anything not provably array-like.
func (tr *smtTranslator) arrayLenSymForAnyElem(expr ast.Expr, env map[string]string) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.arrayLenSymForAnyElem(n.Inner, env)
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				return smtArrayLenOf(bound), true
			}
		}
		if tr.calleeFreeProjection(n, env) {
			return "", false
		}
		t := tr.a.exprType(n)
		if t == nil && tr.a.currentScope != nil {
			if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil {
				t = sym.Type
			}
		}
		if isAnyArrayLike(t) {
			return smtVar(n.Name) + smtArrayLenSuffix, true
		}
	case *ast.FieldExpr:
		t := tr.a.exprType(n)
		if t == nil {
			if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
				t = resolved
			}
		}
		if isAnyArrayLike(t) {
			if env != nil {
				if bound, ok := env[smtProjectionName(n)]; ok {
					return smtArrayLenOf(bound), true
				}
			}
			if tr.calleeFreeProjection(n, env) {
				return "", false
			}
			name, ok := tr.fieldProjectionKey(n, env)
			if !ok {
				return "", false
			}
			return smtVar(name) + smtArrayLenSuffix, true
		}
	}
	return "", false
}

// smtSViewLengthSymbol creates a stable SMT symbol for an immutable view's length.
// Quoted, bijectively encoded names keep these metadata symbols disjoint from Elisa variables
// and from the ordinary array-length symbols emitted by smtVar.
func smtSViewLengthSymbol(projection string) string {
	return "|sview_length_" + base64.RawURLEncoding.EncodeToString([]byte(projection)) + "|"
}

// isAnyArrayLike reports whether t is an array or darray of ANY element type (unlike
// smtTranslator.isArrayLike, which restricts to integer/bool elements for the element-select theory). A
// length read needs only that the value is a sized container, so the element type is irrelevant.
func isAnyArrayLike(t Type) bool {
	switch stripRefForBounds(t).(type) {
	case *ArrayType, *DArrayType:
		return true
	}
	return false
}

func (tr *smtTranslator) recordFixedArrayLen(arr string, t Type) {
	tr.recordArrayElementRange(arr, t)
	at, ok := stripRefForBounds(t).(*ArrayType)
	if !ok || at == nil || !at.HasConstSize {
		return
	}
	tr.lenConsts[arr+smtArrayLenSuffix] = at.ConstSize
}

// recordArrayElementRange notes the value range of an integer-element array's elements: every
// element of a `darray[u32]` is a u32. The range is asserted with the select as its trigger, so
// it is instantiated only at reads the query already makes.
func (tr *smtTranslator) recordArrayElementRange(arr string, t Type) {
	var elem Type
	switch at := stripRefForBounds(t).(type) {
	case *ArrayType:
		if at != nil {
			elem = at.Elem
		}
	case *DArrayType:
		if at != nil {
			elem = at.Elem
		}
	}
	if elem == nil || IsBoolType(elem) || IsFloatType(elem) {
		return
	}
	signed, bits, ok := smtIntWidthSign(elem)
	if !ok || bits <= 0 || bits > 64 {
		return
	}
	lo, hi := "0", smtPow2(bits)
	if signed {
		lo, hi = "(- "+smtPow2(bits-1)+")", smtPow2(bits-1)
	}
	if tr.arrayElementRanges == nil {
		tr.arrayElementRanges = map[string][2]string{}
	}
	tr.arrayElementRanges[arr] = [2]string{lo, hi}
}

// isArrayLike reports whether a type is an integer/bool-element array/darray we can model as (Array Int
// Int). Non-integer elements decline (sound: we only model integer element theory).
func (tr *smtTranslator) isArrayLike(t Type) bool {
	switch at := stripRefForBounds(t).(type) {
	case *ArrayType:
		return at != nil && ((IsNumericType(at.Elem) && !IsFloatType(at.Elem)) || IsBoolType(at.Elem))
	case *DArrayType:
		return at != nil && ((IsNumericType(at.Elem) && !IsFloatType(at.Elem)) || IsBoolType(at.Elem))
	}
	return false
}

// smtKeySort returns the SMT sort string for a set element / dict key type, and whether it is
// soundly modelable. Integer (non-float numeric) -> "Int"; bool -> "Bool". Floats, structs, enums,
// strings, and everything else DECLINE (return ok=false) — modeling them would require theories we
// do not emit, so we forgo the proof rather than risk unsoundness.
func smtKeySort(t Type) (string, bool) {
	t = stripRefForBounds(t)
	if IsBoolType(t) {
		return "Bool", true
	}
	if IsNumericType(t) && !IsFloatType(t) {
		return "Int", true
	}
	return "", false
}

// setTermEnv lowers a set-valued expression to its SMT membership-array symbol (an `(Array <KSort>
// Bool)`), returning the symbol, its key sort, and ok. It resolves a law's `self` / quantifier
// binder through `env`, an identifier/field through its set type. Non-set or non-modelable element
// types decline (sound).
func (tr *smtTranslator) setTermEnv(expr ast.Expr, env map[string]string) (sym string, ksort string, ok bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.setTermEnv(n.Inner, env)
	case *ast.Ident:
		if env != nil {
			if bound, bok := env[n.Name]; bok {
				if ks, kok := tr.setDecls[bound]; kok {
					return bound, ks, true
				}
			}
		}
		st, kok := tr.setElemType(n)
		if !kok {
			return "", "", false
		}
		ks, sok := smtKeySort(st)
		if !sok {
			return "", "", false
		}
		sym = smtVar(n.Name)
		tr.setDecls[sym] = ks
		return sym, ks, true
	case *ast.FieldExpr:
		st, kok := tr.setElemType(n)
		if !kok {
			return "", "", false
		}
		ks, sok := smtKeySort(st)
		if !sok {
			return "", "", false
		}
		name, nok := tr.fieldProjectionKey(n, env)
		if !nok || tr.calleeFreeProjection(n, env) {
			return "", "", false
		}
		sym = smtVar(name)
		tr.setDecls[sym] = ks
		return sym, ks, true
	}
	return "", "", false
}

// keyTermEnv lowers a set-element / dict-key expression to a term of the container's key sort:
// an integer term for sort "Int", a boolean term for sort "Bool". This keeps the `(select <arr>
// <key>)` index well-sorted.
func (tr *smtTranslator) keyTermEnv(expr ast.Expr, ksort string, env map[string]string) (string, bool) {
	if ksort == "Bool" {
		return tr.boolTerm(expr, env)
	}
	return tr.termEnv(expr, env)
}

// setElemType resolves the element type of a set-typed expression (from exprTypes, or the symbol's
// declared type when the node predates body analysis — e.g. precondition/law proving).
func (tr *smtTranslator) setElemType(expr ast.Expr) (Type, bool) {
	t := tr.a.exprType(expr)
	if t == nil && tr.a.currentScope != nil {
		if id, ok := expr.(*ast.Ident); ok {
			if sym, ok := tr.a.currentScope.Lookup(id.Name); ok && sym != nil {
				t = sym.Type
			}
		}
	}
	if st, ok := stripRefForBounds(t).(*SetType); ok && st != nil {
		return st.Elem, true
	}
	return nil, false
}

// dictTermEnv lowers a dict-valued expression to its SMT base symbol plus the key sort and (modeled)
// value type. The dict is realized as two arrays in factPreamble: `<sym>_keys` (membership) and
// `<sym>_vals` (value map). Only int/bool keys and int values are modeled; otherwise decline.
func (tr *smtTranslator) dictTermEnv(expr ast.Expr, env map[string]string) (sym string, ksort string, ok bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.dictTermEnv(n.Inner, env)
	case *ast.Ident:
		if env != nil {
			if bound, bok := env[n.Name]; bok {
				if ks, kok := tr.dictDecls[bound]; kok {
					return bound, ks, true
				}
			}
		}
		return tr.dictDeclFor(n, smtVar(n.Name))
	case *ast.FieldExpr:
		name, ok := tr.fieldProjectionKey(n, env)
		if !ok || tr.calleeFreeProjection(n, env) {
			return "", "", false
		}
		return tr.dictDeclFor(n, smtVar(name))
	}
	return "", "", false
}

// dictDeclFor resolves a dict-typed expression's key/value types and records the base symbol when
// both key and value are soundly modelable (int/bool key, int value).
func (tr *smtTranslator) dictDeclFor(expr ast.Expr, sym string) (string, string, bool) {
	dt, ok := tr.dictType(expr)
	if !ok {
		return "", "", false
	}
	ks, kok := smtKeySort(dt.Key)
	if !kok {
		return "", "", false
	}
	// Value must be an integer (non-float numeric) — that is the only value theory we model. Bool
	// values are not yet supported (the value array is fixed to (Array <KSort> Int)).
	if !IsNumericType(dt.Value) || IsFloatType(dt.Value) {
		return "", "", false
	}
	tr.dictDecls[sym] = ks
	return sym, ks, true
}

// dictMethodCall recognizes the SURFACE dict-access methods in spec position and maps them onto the
// existing two-array dict model (see dictTermEnv). The surface language has no `d[k]` / `k in d`
// syntax for dicts; membership and lookup are expressed through methods that the analyzer resolves to
// the stdlib free functions `arena_dict_contains` / `arena_dict_get`:
//
//	d.contains(k)  ->  arena_dict_contains(&d, k)  ->  (select <d>_keys <k>)      [Bool: membership]
//	d.get(k)       ->  arena_dict_get(&d, k)       ->  (select <d>_vals <k>)      [Int : value]
//
// Both take the dict as the first (receiver) argument and the key as the second. The dict must be
// soundly modelable (int/bool key, int value); otherwise we decline so the obligation falls back to a
// runtime check rather than fabricating a proof.
//
// SOUNDNESS of the `d.get(k)` -> value-array mapping: `arena_dict_get` returns an OPTIONAL value
// (`T?`); for a key not in the dict it is null. Modeling the result as `(select <d>_vals <k>)` (an
// arbitrary-but-total Int) is only sound for a goal that ALSO carries a membership guard for `k`
// (typically `requires d.contains(k)`), which constrains `(select <d>_keys <k>)` to true and binds
// the goal to the in-domain value the quantifier hypothesis ranges over. Without such a guard the
// value is an unconstrained Int and the goal simply will not discharge — never falsely proven.
//
// `kind` is "keys" for the membership method, "vals" for the lookup method. Returns the select term.
func (tr *smtTranslator) dictMethodCall(call *ast.CallExpr, kind string, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	decl = tr.a.readDecl(decl)
	tr.a.addProofReportOrigin(decl)
	if !direct || decl == nil {
		return "", false
	}
	var want string
	switch kind {
	case "keys":
		want = "arena_dict_contains"
	case "vals":
		want = "arena_dict_get"
	default:
		return "", false
	}
	if decl.Name != want {
		return "", false
	}
	args := proofCallArgs(call)
	if len(args) != 2 || args[0] == nil || args[1] == nil {
		return "", false
	}
	// UFCS resolution wraps the receiver in synthetic ref CASTs (`(&d).cast[dict[..]&]`); peel addr-of,
	// parens, and casts down to the underlying dict expression the model is keyed on.
	dictArg := args[0]
	for {
		next := stripAddrAndParens(dictArg)
		if c, ok := next.(*ast.CastExpr); ok && c != nil && c.Operand != nil {
			dictArg = c.Operand
			continue
		}
		dictArg = next
		break
	}
	dsym, ksort, ok := tr.dictTermEnv(dictArg, env)
	if !ok {
		return "", false
	}
	key, ok := tr.keyTermEnv(args[1], ksort, env)
	if !ok {
		return "", false
	}
	return "(select " + dsym + "_" + kind + " " + key + ")", true
}

func (tr *smtTranslator) dictType(expr ast.Expr) (*DictType, bool) {
	t := tr.a.exprType(expr)
	if t == nil && tr.a.currentScope != nil {
		if id, ok := expr.(*ast.Ident); ok {
			if sym, ok := tr.a.currentScope.Lookup(id.Name); ok && sym != nil {
				t = sym.Type
			}
		}
	}
	if dt, ok := stripRefForBounds(t).(*DictType); ok && dt != nil {
		return dt, true
	}
	return nil, false
}

// term lowers an integer-valued expression. Supports literals, immutable integer identifiers
// (declared as SMT Int consts), parenthesization, unary minus, and +/-/* — including the var*var
// PRODUCT the affine prover cannot handle (the headline reason to call the solver). Division and
// modulo are deliberately omitted for now: SMT-LIB `div`/`mod` are Euclidean and would not match
// Elisa's truncating integer division for negative operands, so translating them could be unsound.
func (tr *smtTranslator) term(expr ast.Expr) (string, bool) {
	return tr.termEnv(expr, nil)
}

// sameWidthSignRoundTripEnv cancels a pair of postfix integer conversions that preserve
// the same bits: signed -> unsigned -> signed (or the reverse), with all three types at
// the same width. The backend lowers each same-width conversion to the original integer
// value, and the corresponding bit-vector interpretation pair is exactly the identity.
// Cancelling here avoids asking SMT to rediscover that identity through int2bv/bv2int.
func (tr *smtTranslator) sameWidthSignRoundTripEnv(outer *ast.CastExpr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || outer == nil || !isPureNumericValueCast(outer) {
		return "", false
	}
	inner, ok := stripOptimizationParens(outer.Operand).(*ast.CastExpr)
	if !ok || inner == nil || !isPureNumericValueCast(inner) {
		return "", false
	}
	sourceType := tr.castOperandType(inner)
	sourceSigned, sourceBits, sourceOK := smtIntWidthSign(sourceType)
	innerSigned, innerBits, innerOK := smtIntWidthSign(tr.a.exprType(inner))
	if !innerOK {
		innerSigned, innerBits, innerOK = postfixCastTargetWidthSign(inner)
	}
	outerSigned, outerBits, outerOK := smtIntWidthSign(tr.a.exprType(outer))
	if !outerOK {
		outerSigned, outerBits, outerOK = postfixCastTargetWidthSign(outer)
	}
	if !sourceOK || !innerOK || !outerOK || sourceBits != innerBits || sourceBits != outerBits || sourceSigned == innerSigned || sourceSigned != outerSigned {
		return "", false
	}
	return tr.termEnv(inner.Operand, env)
}
