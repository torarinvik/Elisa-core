package semantic

import (
	"strconv"

	"elisacore/src/ast"
)

// rewriteDStrStringLiteral rewrites a bare string literal whose target is a region-less `dstr`
// into the byte-list literal a user would otherwise hand-write (`['a'.u8(), ...]` ≡ `[97, ...]`).
// `dstr` is the u8 specialization of darray, so a string literal — otherwise a static `cstr` — does
// not assign to it. This is the semantic-phase analogue of the parser's desugarDStrStringLiteralInit
// (which handles the typed var-decl, where the type is syntactic); here it covers ASSIGNMENT
// (`s <- "..."`) and RETURN (`return "..."`) positions, where the dstr target type is only known
// after resolution.
//
// The result is an ordinary allocating list literal that flows through the identical growable-darray
// path (region backing, escape checks, codegen), adding no new surface. StringLit.Value is the
// already-decoded raw byte sequence (escapes resolved by the lexer), so each byte becomes a
// suffix-less IntLit whose type is driven to u8 by the darray[u8] list context (no discouraged-suffix
// warning). An empty string yields an empty `[]`. The rewrite is a no-op unless *value is a StringLit
// and target is a region-less dstr.
//
// Assignment-position list literals need an ambient region; the parser's functionBodyNeedsAutoRegion
// synthesizes one when an AssignStmt value is a StringLit/allocating literal. Returned plain list
// literals allocate region-poly with no scope, so the return position needs no auto-region.
func (a *Analyzer) rewriteDStrStringLiteral(value *ast.Expr, target Type) {
	if value == nil {
		return
	}
	lit, ok := (*value).(*ast.StringLit)
	if !ok || !isDStrType(target) {
		return
	}
	elems := make([]ast.Expr, 0, len(lit.Value))
	for i := 0; i < len(lit.Value); i++ {
		elems = append(elems, &ast.IntLit{Position: lit.Position, Value: strconv.Itoa(int(lit.Value[i]))})
	}
	*value = &ast.ListLitExpr{Position: lit.Position, Elems: elems}
}

// isDStrType reports whether t is the owned `dstr` string — the `&DArrayType{SurfaceName: "dstr"}`
// that `dstr` resolves to — regardless of region. A `cstr`/view or any non-dstr darray is excluded.
// The region is intentionally not checked: a string literal should become a byte list for any dstr
// target (region-less, auto-region-stamped local, or explicit `dstr @r`); the resulting list literal
// is then allocated in whatever region the target/ambient scope dictates. RefType wrappers are
// unwrapped.
func isDStrType(t Type) bool {
	for {
		ref, ok := t.(*RefType)
		if !ok {
			break
		}
		t = ref.Elem
	}
	da, ok := t.(*DArrayType)
	return ok && da.SurfaceName == "dstr"
}
