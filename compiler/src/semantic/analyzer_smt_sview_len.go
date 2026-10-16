package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
)

// bindSMTSViewLengthArg binds an sview formal whose bytes the translator may not model (the
// content gate in immutableSviewArrayParameter: a view's backing can change) by its LENGTH alone.
// A view's length is part of the view value, so at the binding point the formal's `.len` equals the
// argument's `.len`.
//
// The formal is bound to a FRESH byte-array symbol (`v_@svarg$N`; `@` cannot occur in an Elisa
// identifier, so it never collides with a caller or callee name) whose length symbol is asserted
// equal to the argument's length term. Every existing array path (`.len`, `.count`, indexing,
// quantifiers) then treats the formal as an ordinary bound array: the length is exact and the
// contents are unconstrained, so a contents-dependent obligation can only be proven if it holds
// for every byte sequence. Never resolving the formal through its spelling is what keeps a
// same-named caller variable from aliasing it.
func (tr *smtTranslator) bindSMTSViewLengthArg(env map[string]string, name string, argExpr ast.Expr, argEnv map[string]string) bool {
	lenTerm, ok := tr.sviewArgLengthTerm(argExpr, argEnv)
	if !ok {
		return false
	}
	fresh := "@svarg$" + strconv.Itoa(tr.sviewArgCount)
	tr.sviewArgCount++
	tr.arrayDecls[fresh] = true
	arr := smtVar(fresh)
	lenSym := arr + smtArrayLenSuffix
	tr.lenDecls[lenSym] = true
	// "~" sorts after every declaration key in the enum preamble, which follows all declarations.
	tr.enumDecls["~svarg_len_"+fresh] = "(assert (= " + lenSym + " " + lenTerm + "))\n"
	env[name] = arr
	return true
}

// sviewArgLengthTerm translates the length of an sview-valued argument: a view path (`text`,
// `s.name`), or a slice `base[start:end]` of an sview.
//
// A compiled sview slice does NOT trap on bad bounds: it lowers to the runtime's
// string_view_slice, which CLAMPS (start to [0, len], end to [start, len], a non-positive base
// length gives the empty view). The length term reproduces that clamping exactly, including the
// i64 reinterpretation of a 64-bit unsigned bound, so it is the length the program computes on
// every input — never the `end - start` an in-bounds reading would suggest.
func (tr *smtTranslator) sviewArgLengthTerm(argExpr ast.Expr, argEnv map[string]string) (string, bool) {
	expr := stripOptimizationParens(argExpr)
	if slice, ok := expr.(*ast.SliceExpr); ok && slice != nil {
		return tr.sviewSliceLengthTerm(slice, argEnv)
	}
	switch expr.(type) {
	case *ast.Ident, *ast.FieldExpr:
	default:
		return "", false
	}
	return tr.termEnv(&ast.FieldExpr{Position: argExpr.Pos(), Object: argExpr, Field: "len"}, argEnv)
}

// sviewSliceLengthTerm models `base[start:end]` on an sview base as the runtime computes it
// (elisacore_runtime_strings.elisa string_view_slice): with L = base.len and the bounds read as
// i64, lo = clamp(max(start, 0), L), hi = clamp(end >= 0 ? end : L, L) raised to lo, and the
// length is hi - lo, or 0 when L <= 0. Only the two-bound form is modeled (it is the form that
// lowers to that runtime routine); anything else declines.
func (tr *smtTranslator) sviewSliceLengthTerm(slice *ast.SliceExpr, env map[string]string) (string, bool) {
	if slice.Object == nil || slice.Start == nil || slice.End == nil {
		return "", false
	}
	if _, ok := stripRefForBounds(tr.a.exprTypes[slice.Object]).(*SViewType); !ok {
		return "", false
	}
	base, ok := tr.sviewArgLengthTerm(slice.Object, env)
	if !ok {
		return "", false
	}
	start, ok := tr.sviewSliceBoundI64(slice.Start, env)
	if !ok {
		return "", false
	}
	end, ok := tr.sviewSliceBoundI64(slice.End, env)
	if !ok {
		return "", false
	}
	// The view length field is an i64 (StringView {data, i64 len}), so it is below 2^63; without this
	// a usize bound >= 2^63 (read by the runtime as negative) could not be ruled out of [0, L].
	tr.enumDecls["~svlen_i64_"+base] = "(assert (< " + base + " " + smtPow2(63) + "))\n"
	lo0 := "(ite (> " + start + " 0) " + start + " 0)"
	lo := "(ite (> " + lo0 + " " + base + ") " + base + " " + lo0 + ")"
	hi0 := "(ite (>= " + end + " 0) " + end + " " + base + ")"
	hi1 := "(ite (> " + hi0 + " " + base + ") " + base + " " + hi0 + ")"
	hi := "(ite (< " + hi1 + " " + lo + ") " + lo + " " + hi1 + ")"
	return "(ite (<= " + base + " 0) 0 (- " + hi + " " + lo + "))", true
}

// linkSViewArrayLength equates the length of a view minted as a byte array (`v_text`, length
// `v_text_len`) with the view's scope-keyed length symbol, which is what the current function's own
// `text.len` facts are stated over. Both name the same binding's length, so the equation is a
// fact; without it a callee formal bound to `v_text` could not use the caller's `text.len` facts.
func (tr *smtTranslator) linkSViewArrayLength(id *ast.Ident, arr string) {
	key, ok := immutableSViewLengthProjection(tr.a, tr.a.currentScope, &ast.FieldExpr{Position: id.Position, Object: id, Field: "len"})
	if !ok {
		return
	}
	lenSym := smtSViewLengthSymbol(key)
	tr.lenDecls[lenSym] = true
	tr.lenDecls[arr+smtArrayLenSuffix] = true
	tr.enumDecls["~svlen_link_"+arr] = "(assert (= " + arr + smtArrayLenSuffix + " " + lenSym + "))\n"
}

// bindSMTLengthOnlyArg binds a container formal whose ELEMENTS are outside the array theory (a
// darray of views, structs, floats, ...) by its length alone: a fresh token (`v_lenonly$N`) whose
// `_len` is asserted equal to the argument's length. The token is registered in tr.lengthOnly, and
// every array-term lookup that hits it declines, so `.count`/`.len` resolve while any element read
// leaves the fragment instead of being modeled as an unconstrained Int (which would, e.g., make
// `xs[i] == xs[i]` provable for float elements, false for NaN).
func (tr *smtTranslator) bindSMTLengthOnlyArg(env map[string]string, name string, argExpr ast.Expr, argEnv map[string]string) bool {
	lenTerm, ok := tr.arrayLenSymForAnyElem(argExpr, argEnv)
	if !ok {
		return false
	}
	tr.lenDecls[lenTerm] = true
	fresh := smtVar("@lenonly$" + strconv.Itoa(tr.sviewArgCount))
	tr.sviewArgCount++
	if tr.lengthOnly == nil {
		tr.lengthOnly = map[string]bool{}
	}
	tr.lengthOnly[fresh] = true
	tr.lenDecls[fresh+smtArrayLenSuffix] = true
	tr.enumDecls["~lenonly_"+fresh] = "(assert (= " + fresh + smtArrayLenSuffix + " " + lenTerm + "))\n"
	env[name] = fresh
	return true
}

// fieldProjectionKey names the symbol for a field-chain read `root.f.g` that the env did not bind
// directly. Projection symbols are keyed by spelling, so when the env binds the ROOT (a substituted
// callee formal) the spelling `root` denotes the ARGUMENT, not the current function's same-named
// variable:
//   - root bound to a datatype projection token (`__dt__q`, an opaque struct argument `q`): the
//     read is re-rooted at the argument's projection (`q.f.g`), the very symbol the
//     caller's own facts about `q.f.g` use;
//   - root bound any other way (a scalar/array term, a constant string, a variant constructor): the
//     field has no recoverable symbol, so the read declines.
//
// Without this, `need(q)` with `requires k <= p.n` read the CALLER's `p.n` (false proof).
func (tr *smtTranslator) fieldProjectionKey(n ast.Expr, env map[string]string) (string, bool) {
	name := smtProjectionName(n)
	if env == nil {
		return name, true
	}
	root := stripOptimizationParens(n)
	for {
		if f, ok := root.(*ast.FieldExpr); ok && f != nil {
			root = stripOptimizationParens(f.Object)
			continue
		}
		break
	}
	id, ok := root.(*ast.Ident)
	if !ok || id == nil {
		return name, true
	}
	if _, bound := env["\x00string:"+id.Name]; bound {
		return "", false
	}
	bound, ok := env[id.Name]
	if !ok {
		return name, true
	}
	if !strings.HasPrefix(bound, dtTokenPrefix) || strings.HasPrefix(bound, dtTokenPrefix+"enumctor#") {
		return "", false
	}
	return strings.TrimPrefix(bound, dtTokenPrefix) + strings.TrimPrefix(name, id.Name), true
}

// envBindsRoot reports whether expr is an identifier the substitution env binds (as a term or as a
// constant-string binding). Such a name denotes the substituted argument, never the current
// function's same-spelled binding, so scope-keyed lowerings must not resolve it.
func (tr *smtTranslator) envBindsRoot(expr ast.Expr, env map[string]string) bool {
	id, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || id == nil || env == nil {
		return false
	}
	if _, bound := env[id.Name]; bound {
		return true
	}
	_, bound := env["\x00string:"+id.Name]
	return bound
}

// sviewSliceBoundI64 is a slice bound as the i64 the runtime receives: a value of a 64-bit
// unsigned type is reinterpreted (values >= 2^63 become negative); narrower integers and signed
// types are value-preserving. Non-integer or wider types decline.
func (tr *smtTranslator) sviewSliceBoundI64(expr ast.Expr, env map[string]string) (string, bool) {
	signed, bits, ok := smtIntWidthSign(tr.a.exprTypes[expr])
	if !ok || bits > 64 {
		return "", false
	}
	term, ok := tr.termEnv(expr, env)
	if !ok {
		return "", false
	}
	if !signed && bits == 64 {
		return "(ite (>= " + term + " " + smtPow2(63) + ") (- " + term + " " + smtPow2(64) + ") " + term + ")", true
	}
	return term, true
}

// smtArrayLenSuffix names an array's length in a namespace unavailable to source
// identifiers or field paths. Elisa identifiers allow $, but do not allow @.
const smtArrayLenSuffix = "@len"
