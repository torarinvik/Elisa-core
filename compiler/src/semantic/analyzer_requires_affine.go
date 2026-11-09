package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// classifyDiffRange decides a comparison `L OP R` from the interval of the difference `L - R`. A bound
// is used only when known; an open bound on the needed side leaves the verdict requiresUnknown
// (fail-closed). Shared by the affine tier and the field-invariant range tier.
func classifyDiffRange(r numRange, op lexer.TokenKind) requiresVerdict {
	switch op {
	case lexer.TOKEN_GT: // L - R > 0
		if r.loKnown && r.lo > 0 {
			return requiresProven
		}
		if r.hiKnown && r.hi <= 0 {
			return requiresRefuted
		}
	case lexer.TOKEN_GTEQ: // L - R >= 0
		if r.loKnown && r.lo >= 0 {
			return requiresProven
		}
		if r.hiKnown && r.hi < 0 {
			return requiresRefuted
		}
	case lexer.TOKEN_LT: // L - R < 0
		if r.hiKnown && r.hi < 0 {
			return requiresProven
		}
		if r.loKnown && r.lo >= 0 {
			return requiresRefuted
		}
	case lexer.TOKEN_LTEQ: // L - R <= 0
		if r.hiKnown && r.hi <= 0 {
			return requiresProven
		}
		if r.loKnown && r.lo > 0 {
			return requiresRefuted
		}
	case lexer.TOKEN_EQEQ: // L - R == 0
		if r.loKnown && r.hiKnown && r.lo == 0 && r.hi == 0 {
			return requiresProven
		}
		if (r.loKnown && r.lo > 0) || (r.hiKnown && r.hi < 0) {
			return requiresRefuted
		}
	case lexer.TOKEN_BANGEQ: // L - R != 0
		if (r.loKnown && r.lo > 0) || (r.hiKnown && r.hi < 0) {
			return requiresProven
		}
		if r.loKnown && r.hiKnown && r.lo == 0 && r.hi == 0 {
			return requiresRefuted
		}
	}
	return requiresUnknown
}

// subNumRange returns the interval of `l - r`: [l.lo - r.hi, l.hi - r.lo]. A bound is known only when
// both contributing bounds are known and the subtraction does not overflow int64 (an overflowing bound
// becomes open, which only declines a proof — never admits an unsound one).
func subNumRange(l, r numRange) numRange {
	out := numRange{}
	if l.loKnown && r.hiKnown {
		if v, ok := subInt64Checked(l.lo, r.hi); ok {
			out.loKnown, out.lo = true, v
		}
	}
	if l.hiKnown && r.loKnown {
		if v, ok := subInt64Checked(l.hi, r.lo); ok {
			out.hiKnown, out.hi = true, v
		}
	}
	return out
}

func subInt64Checked(a, b int64) (int64, bool) {
	if b == -int64(^uint64(0)>>1)-1 { // b == minInt64: -b overflows, so decline (open bound)
		return 0, false
	}
	return addInt64Checked(a, -b)
}

// rangeOfRequiresOperand bounds a precondition operand as an integer interval: first via the affine
// tier (constants, immutable-int params/args, linear combos), then — when that yields no usable bound —
// via the struct invariant of a field-read operand (resolved directly or through a substituted param).
func (a *Analyzer) rangeOfRequiresOperand(expr ast.Expr, subst map[string]ast.Expr) (numRange, bool) {
	if aff, ok := a.substitutedAffine(expr, subst); ok {
		r := a.boundAffine(aff, a.currentScope)
		if r.loKnown || r.hiKnown {
			return r, true
		}
	}
	if fe, ok := a.resolveRequiresFieldOperand(expr, subst); ok {
		if r, ok := a.structInvariantFieldRange(fe); ok {
			return r, true
		}
	}
	return numRange{}, false
}

// resolveRequiresFieldOperand resolves a precondition operand to the caller-side field read it denotes:
// either the operand is itself a field read, or it is a callee parameter whose substituted argument is
// a field read (`requires slot >= 0` called as `f(c.panel_idx)`). Returns the field expression whose
// `Object` type carries the struct invariant.
func (a *Analyzer) resolveRequiresFieldOperand(expr ast.Expr, subst map[string]ast.Expr) (*ast.FieldExpr, bool) {
	switch n := unwrapParen(expr).(type) {
	case *ast.FieldExpr:
		return n, true
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			if fe, ok := unwrapParen(arg).(*ast.FieldExpr); ok {
				return fe, true
			}
		}
	}
	return nil, false
}

// transitiveRelationalProof attempts to prove or refute a comparison `leftAffine OP rightAffine`
// by transitive closure over the relational-edge graph (requires-clauses and flow-local
// assert-facts between immutable integer idents). Both sides must reduce to a single immutable
// integer variable (affine form with exactly one term of coefficient +1 and constant=0); any
// more complex form is outside this tier and returns requiresUnknown (sound: we only conclude
// when we can chain a witnessed path).
//
// Sound: a path of `<=` edges witnesses `a <= c`; a path with at least one `<` edge witnesses
// `a < c` (which also proves `a <= c`). Refutation of `a >= c` follows from proof of `a < c`.
// A missing link returns requiresUnknown (declines, never fabricates).
func (a *Analyzer) transitiveRelationalProof(leftAff, rightAff affineForm, op lexer.TokenKind) requiresVerdict {
	// Only ident-vs-ident with unit coefficient and zero constant.
	if leftAff.c != 0 || len(leftAff.terms) != 1 {
		return requiresUnknown
	}
	if rightAff.c != 0 || len(rightAff.terms) != 1 {
		return requiresUnknown
	}
	var lName, rName string
	for k, v := range leftAff.terms {
		if v != 1 {
			return requiresUnknown
		}
		lName = k
	}
	for k, v := range rightAff.terms {
		if v != 1 {
			return requiresUnknown
		}
		rName = k
	}
	if lName == "" || rName == "" {
		return requiresUnknown
	}

	// Build the relational-edge graph.
	edges := a.collectRelationalEdges()
	if len(edges) == 0 {
		return requiresUnknown
	}

	// BFS from lName, following edges that give UPPER bounds (lName <= ... <= rName), to see if
	// rName is reachable with a path consisting of <= or < edges. We also track whether any edge
	// on the path is strict (< rather than <=) so we know if we proved `lName < rName` or just
	// `lName <= rName`.
	//
	// Edge direction: for `a <= b` (LTEQ with left=a), following from a reaches b with `<=` weight.
	//                 for `a < b`  (LT   with left=a), following from a reaches b with `<`  weight.
	//                 for `b >= a` (GTEQ with right=a), following from a reaches b with `<=` weight.
	//                 for `b > a`  (GT   with right=a), following from a reaches b with `<`  weight.
	type bfsState struct {
		node   string
		strict bool // at least one < edge on the path so far
		depth  int
	}
	const maxDepth = 8
	// visited tracks (node, strict) — once we've seen a node with strict=false, we need not
	// revisit with strict=true (it's a tighter result either way, and we take the best we find).
	visitedNonStrict := map[string]bool{}
	visitedStrict := map[string]bool{}
	queue := []bfsState{{node: lName, strict: false, depth: 0}}
	foundStrict := false
	foundNonStrict := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, e := range edges {
			var peer string
			var edgeStrict bool
			switch e.op {
			case lexer.TOKEN_LTEQ:
				if e.left == cur.node {
					peer, edgeStrict = e.right, false
				}
			case lexer.TOKEN_LT:
				if e.left == cur.node {
					peer, edgeStrict = e.right, true
				}
			case lexer.TOKEN_GTEQ:
				if e.right == cur.node {
					peer, edgeStrict = e.left, false
				}
			case lexer.TOKEN_GT:
				if e.right == cur.node {
					peer, edgeStrict = e.left, true
				}
			}
			if peer == "" {
				continue
			}
			pathStrict := cur.strict || edgeStrict
			if peer == rName {
				if pathStrict {
					foundStrict = true
				} else {
					foundNonStrict = true
				}
				continue
			}
			// Avoid revisiting a node on a path that is not more informative.
			if pathStrict {
				if visitedStrict[peer] {
					continue
				}
				visitedStrict[peer] = true
			} else {
				if visitedNonStrict[peer] {
					continue
				}
				visitedNonStrict[peer] = true
			}
			queue = append(queue, bfsState{node: peer, strict: pathStrict, depth: cur.depth + 1})
		}
	}

	// Interpret the reachability results against the requested operator.
	// foundStrict implies lName < rName; foundNonStrict implies lName <= rName (not necessarily <).
	provenLT := foundStrict
	provenLTEQ := foundStrict || foundNonStrict
	switch op {
	case lexer.TOKEN_LTEQ: // want lName <= rName
		if provenLTEQ {
			return requiresProven
		}
	case lexer.TOKEN_LT: // want lName < rName
		if provenLT {
			return requiresProven
		}
	case lexer.TOKEN_GTEQ: // want lName >= rName — provably FALSE if lName < rName
		if provenLT {
			return requiresRefuted
		}
	case lexer.TOKEN_GT: // want lName > rName — provably FALSE if lName <= rName
		if provenLTEQ {
			return requiresRefuted
		}
	}
	return requiresUnknown
}

// substitutedAffine builds the affine form of a callee-side expression after replacing each callee
// parameter identifier with the caller's argument affine form. A param identifier resolves through
// `subst` (to the caller's argument, evaluated in the caller scope); any other identifier is treated
// as a potential caller-visible constant (module const) or declines. Mirrors affineOf, plus the
// substitution leaf and a more general product rule (either operand may reduce to a constant form).
func (a *Analyzer) substitutedAffine(expr ast.Expr, subst map[string]ast.Expr) (affineForm, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.substitutedAffine(n.Inner, subst)
	case *ast.IntLit:
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		return affineForm{}, false
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			return a.affineOf(arg, a.currentScope)
		}
		// Not a parameter: only admissible if it const-folds (e.g. a module const referenced by the
		// requires clause). A callee local cannot appear in a requires clause, so this is safe.
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		return affineForm{}, false
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return affineForm{}, false
		}
		inner, ok := a.substitutedAffine(n.Operand, subst)
		if !ok {
			return affineForm{}, false
		}
		return negateAffine(inner), true
	case *ast.CastExpr:
		field, ok := stripOptimizationParens(n.Operand).(*ast.FieldExpr)
		if !ok || field == nil {
			return affineForm{}, false
		}
		base, ok := field.Object.(*ast.Ident)
		if !ok || base == nil {
			return affineForm{}, false
		}
		rebased := field
		if arg, found := subst[base.Name]; found {
			argIdent, ok := unwrapParen(arg).(*ast.Ident)
			if !ok || argIdent == nil {
				return affineForm{}, false
			}
			rebased = &ast.FieldExpr{Position: field.Position, Object: argIdent, Field: field.Field}
		}
		key, ok := affineSViewLengthCastKey(a, a.currentScope, n, rebased)
		if !ok {
			return affineForm{}, false
		}
		return affineForm{terms: map[string]int64{key: 1}}, true
	case *ast.BinaryExpr:
		l, ok := a.substitutedAffine(n.Left, subst)
		if !ok {
			return affineForm{}, false
		}
		r, ok := a.substitutedAffine(n.Right, subst)
		if !ok {
			return affineForm{}, false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			return addAffine(l, r), true
		case lexer.TOKEN_MINUS:
			return subtractAffine(l, r), true
		case lexer.TOKEN_STAR:
			// Linear only when one operand reduces to a pure constant form.
			if len(l.terms) == 0 {
				return scaleAffineForm(r, l.c), true
			}
			if len(r.terms) == 0 {
				return scaleAffineForm(l, r.c), true
			}
			return affineForm{}, false // variable*variable: non-linear, decline (sound)
		default:
			return affineForm{}, false
		}
	case *ast.FieldExpr:
		// A field place in a requires clause (`requires off + 4 <= v.size`): resolve the formal `v`
		// to its caller argument, then recover that argument's construction-time field constant.
		base, ok := n.Object.(*ast.Ident)
		if !ok || base == nil {
			return affineForm{}, false
		}
		argName := base.Name
		fieldExpr := ast.Expr(n)
		if arg, ok := subst[base.Name]; ok {
			argIdent, ok2 := unwrapParen(arg).(*ast.Ident)
			if !ok2 || argIdent == nil {
				// The caller argument is not a bare ident (e.g. an inline literal).  For the
				// `.count` field specifically, try to evaluate the count directly from the arg.
				if n.Field == "count" {
					if list, ok3 := unwrapParen(arg).(*ast.ListLitExpr); ok3 && list != nil {
						return affineForm{c: int64(len(list.Elems)), terms: map[string]int64{}}, true
					}
				}
				return affineForm{}, false
			}
			argName = argIdent.Name
			fieldExpr = &ast.FieldExpr{Position: n.Position, Object: argIdent, Field: n.Field}
		}
		// `sview.len` is a stable numeric projection. Resolve the callee-side
		// parameter to the caller's view binding before constructing the symbolic
		// affine term, so an unchanged view cancels in recursive measures.
		if n.Field == "len" {
			if key, ok := immutableSViewLengthProjection(a, a.currentScope, fieldExpr); ok {
				return affineForm{terms: map[string]int64{key: 1}}, true
			}
		}
		// `xs.count` where `xs` is an immutable darray local initialised from a list literal.
		if n.Field == "count" {
			if cnt, ok2 := a.lookupWrittenListCount(argName); ok2 {
				return affineForm{c: cnt, terms: map[string]int64{}}, true
			}
		}
		fieldVal, ok2 := a.lookupWrittenStructField(argName, n.Field)
		if !ok2 {
			return affineForm{}, false
		}
		return a.affineOf(fieldVal, a.currentScope)
	default:
		return affineForm{}, false
	}
}

// --- affineForm value helpers (pure, allocate fresh maps) -------------------------------------

func addAffine(l, r affineForm) affineForm {
	out := affineForm{c: l.c + r.c, terms: map[string]int64{}}
	for k, v := range l.terms {
		out.addTerm(k, v)
	}
	for k, v := range r.terms {
		out.addTerm(k, v)
	}
	return out
}

func subtractAffine(l, r affineForm) affineForm {
	out := affineForm{c: l.c - r.c, terms: map[string]int64{}}
	for k, v := range l.terms {
		out.addTerm(k, v)
	}
	for k, v := range r.terms {
		out.addTerm(k, -v)
	}
	return out
}

func negateAffine(f affineForm) affineForm {
	out := affineForm{c: -f.c, terms: map[string]int64{}}
	for k, v := range f.terms {
		out.addTerm(k, -v)
	}
	return out
}

func scaleAffineForm(f affineForm, k int64) affineForm {
	out := affineForm{c: f.c * k, terms: map[string]int64{}}
	for name, v := range f.terms {
		out.addTerm(name, v*k)
	}
	return out
}

// qualifyCalleeClauseConsts rewrites a callee's own module constants into the spelling the
// CALLER can see, so a cross-module precondition can actually be discharged.
//
// A clause is written in the callee's namespace: `requires last >= ID_MIN` inside
// `module Ids`. Substituting the argument gives the goal `cursor >= ID_MIN`, which is then
// proven against the caller's facts -- and those facts name the same constant
// `Ids::ID_MIN`, because that is how the caller had to spell it. The two terms never
// unified, so a guard that literally IS the precondition proved nothing and every
// cross-module call with a `requires` fell back to a runtime check plus a lint. Within one
// module the spellings happen to agree, which is why this only ever showed up across a
// module boundary.
//
// Only a name the caller cannot already resolve is rewritten, and only to a constant that
// really exists in the callee's namespace: a caller-visible name keeps its own meaning.
func (a *Analyzer) qualifyCalleeClauseConsts(call *ast.CallExpr, clauses []ast.Expr, subst map[string]ast.Expr) {
	if a == nil || call == nil || len(clauses) == 0 || subst == nil {
		return
	}
	ft, ok := a.callFuncType(call)
	if !ok || ft == nil {
		return
	}
	dot := strings.LastIndex(ft.Name, ".")
	if dot <= 0 {
		return
	}
	namespace := ft.Name[:dot]
	for _, clause := range clauses {
		collectClauseIdents(clause, func(ident *ast.Ident) {
			if ident == nil || ident.Name == "" || subst[ident.Name] != nil {
				return
			}
			if _, ok := a.lookupConstEvalValue(ident.Name); ok {
				return
			}
			if _, ok := a.lookupVisibleConst(ident.Name); ok {
				return
			}
			qualified := namespace + "." + ident.Name
			if _, ok := a.constValues[qualified]; !ok {
				return
			}
			subst[ident.Name] = &ast.Ident{Position: ident.Position, Name: qualified}
		})
	}
}

// collectClauseIdents visits the bare identifiers of a precondition clause. It covers the
// expression forms the bounded-linear prover accepts; anything else it does not descend
// into simply keeps the status quo, since the prover declines on those forms anyway.
func collectClauseIdents(expr ast.Expr, visit func(*ast.Ident)) {
	switch n := expr.(type) {
	case nil:
		return
	case *ast.Ident:
		visit(n)
	case *ast.ParenExpr:
		collectClauseIdents(n.Inner, visit)
	case *ast.UnaryExpr:
		collectClauseIdents(n.Operand, visit)
	case *ast.BinaryExpr:
		collectClauseIdents(n.Left, visit)
		collectClauseIdents(n.Right, visit)
	case *ast.CastExpr:
		collectClauseIdents(n.Operand, visit)
	case *ast.FieldExpr:
		collectClauseIdents(n.Object, visit)
	case *ast.IndexExpr:
		collectClauseIdents(n.Object, visit)
		collectClauseIdents(n.Index, visit)
	case *ast.CallExpr:
		for _, arg := range n.Args {
			collectClauseIdents(arg, visit)
		}
	}
}
