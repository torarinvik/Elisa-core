package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type indexBoundFact struct {
	Upper string
	// NonNeg records that the index is provably >= 0. An upper-bound proof
	// (i < count) is NOT sufficient for a SIGNED index, because a negative value
	// still satisfies i < count and would index out of bounds. Unsigned index
	// types are non-negative by construction; signed indices need this flag set
	// by a `0..<` loop start or an explicit i >= 0 / i > -1 guard.
	NonNeg bool
}

func cloneIndexBoundFacts(src map[string]indexBoundFact) map[string]indexBoundFact {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]indexBoundFact, len(src))
	for name, fact := range src {
		out[name] = fact
	}
	return out
}

func cloneViewStaticLen(src map[string]int64) map[string]int64 {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]int64, len(src))
	for name, length := range src {
		out[name] = length
	}
	return out
}

// constSliceLength returns the statically-known element count of a slice when
// both bounds const-evaluate to non-negative integers with start <= end. The
// resulting view then carries a provable length for zero-cost constant indexing.
func (a *Analyzer) constSliceLength(expr *ast.SliceExpr) (int64, bool) {
	if expr == nil {
		return 0, false
	}
	start, startOK := a.evalConstExpr(expr.Start)
	end, endOK := a.evalConstExpr(expr.End)
	if !startOK || !endOK || start.Kind != ConstInt || end.Kind != ConstInt {
		return 0, false
	}
	if start.Int < 0 || end.Int < start.Int {
		return 0, false
	}
	return end.Int - start.Int, true
}

// recordViewStaticLenBinding records that a freshly-bound view variable came
// from a constant-bounded slice and therefore has a statically-known length.
func (a *Analyzer) recordViewStaticLenBinding(name string, value ast.Expr, bindingType Type) {
	if a == nil || name == "" || value == nil {
		return
	}
	inner := stripOptimizationParens(value)
	if get, ok := inner.(*ast.GetExpr); ok && get != nil {
		// `x = get arr[a:b]` carries the bounded view's length to x.
		inner = stripOptimizationParens(get.Value)
	}
	slice, ok := inner.(*ast.SliceExpr)
	if !ok {
		// A binding to a non-slice expression clears any stale length fact.
		delete(a.currentViewStaticLen, name)
		return
	}
	switch StripAggregateStateType(bindingType).(type) {
	case *ViewType, *DArrayViewType:
	default:
		delete(a.currentViewStaticLen, name)
		return
	}
	if length, ok := a.constSliceLength(slice); ok {
		if a.currentViewStaticLen == nil {
			a.currentViewStaticLen = make(map[string]int64)
		}
		a.currentViewStaticLen[name] = length
		return
	}
	delete(a.currentViewStaticLen, name)
}

func (a *Analyzer) applyIndexBoundsFactsForCondition(cond ast.Expr, truthy bool) {
	facts := indexBoundsFactsForCondition(cond, truthy)
	if len(facts) == 0 {
		return
	}
	if a.currentIndexBounds == nil {
		a.currentIndexBounds = make(map[string]indexBoundFact, len(facts))
	}
	for name, fact := range facts {
		existing := a.currentIndexBounds[name]
		if fact.Upper != "" {
			existing.Upper = fact.Upper
		}
		if fact.NonNeg {
			existing.NonNeg = true
		}
		if existing.Upper == "" && !existing.NonNeg {
			continue
		}
		a.currentIndexBounds[name] = existing
	}
}

// invalidateIndexBoundsForAssignedTarget drops cached index-bounds proofs that a
// reassignment could falsify (deep audit #5): reassigning the index variable
// itself, or reassigning a container that some proof's upper bound refers to.
// Without this, `if i < arr.count: ... i <- i + 99; arr[i]` keeps treating the
// index as in-bounds.
func (a *Analyzer) invalidateIndexBoundsForAssignedTarget(target ast.Expr) {
	if a == nil || len(a.currentIndexBounds) == 0 || target == nil {
		return
	}
	base := optimizationExprString(target)
	if base == "" {
		return
	}
	// If the index variable itself is reassigned, its upper-bound proof no longer
	// holds for the new value.
	delete(a.currentIndexBounds, base)
	// A reassigned view binding may no longer be the constant-bounded slice that
	// established its static length.
	delete(a.currentViewStaticLen, base)
	a.invalidateIndexBoundsReferencingBase(base)
}

// invalidateIndexBoundsForContainer drops proofs whose upper bound refers to a
// container that was just mutated (push/extend/reserve/clear/truncate), since the
// container's count/len changed (deep audit #5).
func (a *Analyzer) invalidateIndexBoundsForContainer(container ast.Expr) {
	if a == nil || len(a.currentIndexBounds) == 0 || container == nil {
		return
	}
	a.invalidateIndexBoundsReferencingBase(optimizationExprString(container))
}

func (a *Analyzer) invalidateIndexBoundsReferencingBase(base string) {
	if a == nil || len(a.currentIndexBounds) == 0 || base == "" {
		return
	}
	for name, fact := range a.currentIndexBounds {
		if indexBoundUpperReferencesBase(fact.Upper, base) {
			delete(a.currentIndexBounds, name)
		}
	}
}

// indexBoundUpperReferencesBase reports whether an upper-bound string is derived
// from a given container/index base, with an identifier boundary so that base
// "arr" matches "arr.count" but not "array.count".
func indexBoundUpperReferencesBase(upper, base string) bool {
	if upper == "" || base == "" {
		return false
	}
	if upper == base {
		return true
	}
	if !strings.HasPrefix(upper, base) {
		return false
	}
	c := upper[len(base)]
	isIdentChar := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	return !isIdentChar
}

func (a *Analyzer) indexExprRequiresUncheckedIndexPermission(expr *ast.IndexExpr) bool {
	if expr == nil {
		return false
	}
	if a.indexBoundsProven[expr] {
		return false
	}
	return indexableTypeRequiresBoundsProof(a.exprTypes[expr.Object])
}

func (a *Analyzer) markIndexBoundsProof(expr *ast.IndexExpr, objType Type) {
	if expr == nil {
		return
	}
	if expr.Fallback != nil {
		a.indexBoundsProven[expr] = true
		return
	}
	if a.indexExprHasBoundsProof(expr, objType) {
		a.indexBoundsProven[expr] = true
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
	}
	// A view bound from a constant-bounded slice has a statically-known length;
	// a constant index within [0, len) is provably in bounds (zero-cost).
	if base, ok := stripOptimizationParens(expr.Object).(*ast.Ident); ok && base != nil {
		if length, known := a.currentViewStaticLen[base.Name]; known {
			switch StripAggregateStateType(stripRefForBounds(objType)).(type) {
			case *ViewType, *DArrayViewType:
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
	if fact.Upper != indexableUpperBoundString(expr.Object, objType) {
		return false
	}
	// An upper-bound proof alone is unsound for a SIGNED index: a negative value
	// also satisfies i < count and indexes out of bounds. Require a proven
	// non-negativity (unsigned index type, a 0..< loop, or an i >= 0 guard).
	if !indexTypeGuaranteedNonNegative(a.exprTypes[expr.Index]) && !fact.NonNeg {
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
	case *ArrayType, *DArrayType, *DArrayViewType, *ViewType, *DStrType, *SViewType:
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
	case *DArrayViewType, *ViewType, *DStrType, *SViewType:
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
