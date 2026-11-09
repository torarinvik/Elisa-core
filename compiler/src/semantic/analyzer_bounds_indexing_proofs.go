package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) indexExprRequiresUncheckedIndexPermission(expr *ast.IndexExpr) bool {
	if expr == nil {
		return false
	}
	if a.indexBoundsProven[expr] {
		return false
	}
	return indexableTypeRequiresBoundsProof(a.exprType(expr.Object))
}

func (a *Analyzer) markIndexBoundsProof(expr *ast.IndexExpr, objType Type) {
	if expr == nil {
		return
	}
	if expr.Fallback != nil {
		a.recordIndexBoundsProof(expr)
		return
	}
	// A summary-inference walk (return provenance, borrowed-owner refs, termination typing) runs on
	// demand, at whatever point a caller first needs the summary, so its facts depend on body
	// analysis order. The proof map only grows and the backend elides the check from it, so only the
	// ordinary body pass, which runs once per body in a fixed context, may record a proof.
	if a.suppressOptimizationFacts {
		return
	}
	if a.indexExprHasBoundsProof(expr, objType) {
		a.recordIndexBoundsProof(expr)
	}
}

func (a *Analyzer) indexExprHasBoundsProof(expr *ast.IndexExpr, objType Type) bool {
	if expr == nil {
		return false
	}
	if array, ok := stripRefForBounds(objType).(*ArrayType); ok && array != nil {
		if value, ok := a.evalConstExpr(expr.Index); ok && value.Kind == ConstInt {
			return value.Int >= 0 && (!array.HasConstSize || value.Int < array.ConstSize)
		}
		// A REFINEMENT-typed index whose proven interval [lo,hi] lies within [0, ConstSize) is in
		// bounds with no runtime check (e.g. `regs[d.sdst]`, sdst : u32 is InRange[0,127], into [128]).
		if array.HasConstSize {
			if lo, hi, ok := a.indexExprRefinementBounds(expr.Index); ok && lo >= 0 && hi < array.ConstSize {
				return true
			}
		}
	}
	// A REFINEMENT-typed index into an IMMUTABLE dynamic array whose length is pinned above the
	// index's proven maximum by a live `requires <darray>.count >= K` precondition is in bounds with
	// no runtime check (the darray analogue of the constant-size case above — e.g. `regs[r]`,
	// r : InRange[0,31], where `requires regs.count >= 32`). Immutability is mandatory: a `mutable`
	// darray could pop/clear and shrink count below K. Sound: a readonly param's count cannot change
	// in the body, the index is proven in [0, hi], and the precondition gives count > hi at entry.
	if _, ok := stripRefForBounds(objType).(*DArrayType); ok {
		if lo, hi, rok := a.indexExprRefinementBounds(expr.Index); rok && lo >= 0 {
			if a.objectIsImmutableDArrayParam(expr.Object) {
				if k, kok := a.liveRequiresCountLowerBound(expr.Object); kok && k > hi {
					return true
				}
			}
		}
	}
	// A view bound from a constant-bounded slice has a statically-known length;
	// a constant index within [0, len) is provably in bounds (zero-cost).
	if base, ok := stripOptimizationParens(expr.Object).(*ast.Ident); ok && base != nil {
		if length, known := a.currentViewStaticLen[base.Name]; known {
			switch StripAggregateStateType(stripRefForBounds(objType)).(type) {
			case *ViewType:
				if value, ok := a.evalConstExpr(expr.Index); ok && value.Kind == ConstInt {
					return value.Int >= 0 && value.Int < length
				}
			}
		}
	}
	indexName, ok := stripOptimizationParens(expr.Index).(*ast.Ident)
	if !ok || indexName == nil {
		return false
	}
	fact, ok := a.currentIndexBounds[indexName.Name]
	if !ok || fact.Upper == "" {
		return false
	}
	want := indexableUpperBoundString(expr.Object, objType)
	// The index's proven upper bound must equal the container's length expression — either
	// syntactically, or via a flow-known equality (`n == xs.count`), which is the cross-variable
	// coupling that lets `for i in 0..<n: xs[i]` discharge when `n` is known equal to `xs.count`.
	if fact.Upper != want && !a.boundExprEqual(fact.Upper, want) {
		return false
	}
	// An upper-bound proof alone is unsound for a SIGNED index: a negative value
	// also satisfies i < count and indexes out of bounds. Require a proven
	// non-negativity (unsigned index type, a 0..< loop, or an i >= 0 guard).
	if !indexTypeGuaranteedNonNegative(a.exprType(expr.Index)) && !fact.NonNeg {
		return false
	}
	return true
}

// indexTypeGuaranteedNonNegative reports whether an index value cannot be
// negative purely by its type (unsigned integers). Signed and unknown types need
// a flow-derived non-negativity fact instead.
func indexTypeGuaranteedNonNegative(t Type) bool {
	b, ok := t.(*BuiltinType)
	if !ok || b == nil {
		return false
	}
	switch b.Name {
	case "usize", "u8", "u16", "u32", "u64", "uintptr":
		return true
	}
	return false
}

// boundConstIntValue evaluates a literal integer (optionally negated) used in a
// comparison, so non-negativity guards like `i >= 0` / `i > -1` can be recognized
// soundly. Non-literals return ok=false (conservative: no fact recorded).
func boundConstIntValue(expr ast.Expr) (int64, bool) {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.IntLit:
		v, err := strconv.ParseInt(n.Value, 0, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_MINUS {
			if v, ok := boundConstIntValue(n.Operand); ok {
				return -v, true
			}
		}
	}
	return 0, false
}

func indexableTypeRequiresBoundsProof(t Type) bool {
	if IsInvalidType(t) {
		return false
	}
	if ref, ok := t.(*RefType); ok && ref != nil && (IsNumericType(ref.Elem) || IsBoolType(ref.Elem)) {
		return true
	}
	switch stripRefForBounds(t).(type) {
	case *ArrayType, *DArrayType, *ViewType, *CStrType, *SViewType:
		return true
	}
	return false
}

func stripRefForBounds(t Type) Type {
	if ref, ok := t.(*RefType); ok && ref != nil && ref.State == RefStateNonNull {
		return ref.Elem
	}
	return t
}

func indexableUpperBoundString(obj ast.Expr, objType Type) string {
	base := optimizationExprString(obj)
	if base == "" {
		return ""
	}
	switch tt := stripRefForBounds(objType).(type) {
	case *ArrayType:
		if tt != nil && tt.HasConstSize {
			return strconv.FormatInt(tt.ConstSize, 10)
		}
	case *DArrayType:
		return base + ".count"
	case *ViewType, *CStrType, *SViewType:
		return base + ".len"
	}
	return ""
}

func indexBoundsFactsForCondition(cond ast.Expr, truthy bool) map[string]indexBoundFact {
	facts := make(map[string]indexBoundFact)
	collectIndexBoundsFacts(cond, truthy, facts)
	if len(facts) == 0 {
		return nil
	}
	return facts
}

func collectIndexBoundsFacts(cond ast.Expr, truthy bool, facts map[string]indexBoundFact) {
	switch n := stripOptimizationParens(cond).(type) {
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			collectIndexBoundsFacts(n.Operand, !truthy, facts)
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				collectIndexBoundsFacts(n.Left, true, facts)
				collectIndexBoundsFacts(n.Right, true, facts)
			}
		case lexer.TOKEN_OR:
			if !truthy {
				collectIndexBoundsFacts(n.Left, false, facts)
				collectIndexBoundsFacts(n.Right, false, facts)
			}
		case lexer.TOKEN_LT:
			if truthy {
				recordStrictUpperBoundFact(n.Left, n.Right, facts)
				// `C < i` with C >= -1 proves i >= 0.
				if c, ok := boundConstIntValue(n.Left); ok && c >= -1 {
					recordNonNegFact(n.Right, facts)
				}
			}
		case lexer.TOKEN_GT:
			if truthy {
				recordStrictUpperBoundFact(n.Right, n.Left, facts)
				// `i > C` with C >= -1 proves i >= 0.
				if c, ok := boundConstIntValue(n.Right); ok && c >= -1 {
					recordNonNegFact(n.Left, facts)
				}
			}
		case lexer.TOKEN_GTEQ:
			if !truthy {
				recordStrictUpperBoundFact(n.Left, n.Right, facts)
			} else if c, ok := boundConstIntValue(n.Right); ok && c >= 0 {
				// `i >= C` with C >= 0 proves i >= 0.
				recordNonNegFact(n.Left, facts)
			}
		case lexer.TOKEN_LTEQ:
			if !truthy {
				recordStrictUpperBoundFact(n.Right, n.Left, facts)
			} else if c, ok := boundConstIntValue(n.Left); ok && c >= 0 {
				// `C <= i` with C >= 0 proves i >= 0.
				recordNonNegFact(n.Right, facts)
			}
		}
	}
}

func recordStrictUpperBoundFact(index ast.Expr, upper ast.Expr, facts map[string]indexBoundFact) {
	name, ok := boundIndexName(index)
	if !ok {
		return
	}
	upperString := optimizationExprString(upper)
	if upperString == "" {
		return
	}
	fact := facts[name]
	fact.Upper = upperString
	facts[name] = fact
}

func recordNonNegFact(index ast.Expr, facts map[string]indexBoundFact) {
	name, ok := boundIndexName(index)
	if !ok {
		return
	}
	fact := facts[name]
	fact.NonNeg = true
	facts[name] = fact
}

func boundIndexName(expr ast.Expr) (string, bool) {
	ident, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || ident == nil || ident.Name == "" {
		return "", false
	}
	return ident.Name, true
}
