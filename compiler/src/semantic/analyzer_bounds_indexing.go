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

// cloneBoundEqual deep-copies the bound-equality adjacency so a branch/loop scope edits its own
// copy (mirrors cloneIndexBoundFacts; rides the same save/restore sites).
func cloneBoundEqual(src map[string]map[string]bool) map[string]map[string]bool {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]map[string]bool, len(src))
	for k, neighbors := range src {
		cp := make(map[string]bool, len(neighbors))
		for n := range neighbors {
			cp[n] = true
		}
		out[k] = cp
	}
	return out
}

// recordBoundEqual adds a symmetric `x == y` fact between two canonical upper-bound expression
// strings. Empty or self equalities are ignored.
func (a *Analyzer) recordBoundEqual(x, y string) {
	if x == "" || y == "" || x == y {
		return
	}
	if a.currentBoundEqual == nil {
		a.currentBoundEqual = map[string]map[string]bool{}
	}
	if a.currentBoundEqual[x] == nil {
		a.currentBoundEqual[x] = map[string]bool{}
	}
	if a.currentBoundEqual[y] == nil {
		a.currentBoundEqual[y] = map[string]bool{}
	}
	a.currentBoundEqual[x][y] = true
	a.currentBoundEqual[y][x] = true
}

// boundExprEqual reports whether two upper-bound expression strings are provably equal under the
// current flow-sensitive equivalence relation (reflexive + transitive closure via a small BFS). The
// relation is tiny in practice, so the unbounded walk is cheap and need not be memoized globally.
func (a *Analyzer) boundExprEqual(x, y string) bool {
	if x == y {
		return true
	}
	if len(a.currentBoundEqual) == 0 || x == "" || y == "" {
		return false
	}
	visited := map[string]bool{x: true}
	queue := []string{x}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range a.currentBoundEqual[cur] {
			if next == y {
				return true
			}
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// invalidateBoundEqualReferencingBase drops every equality fact mentioning a mutated base, so a
// `n == xs.count` fact cannot survive an `xs.push(..)` that changes the count. Called from the same
// invalidation functions as the index-bound facts (so it shares their mutation-site coverage).
func (a *Analyzer) invalidateBoundEqualReferencingBase(base string) {
	if a == nil || len(a.currentBoundEqual) == 0 || base == "" {
		return
	}
	for key, neighbors := range a.currentBoundEqual {
		if indexBoundUpperReferencesBase(key, base) {
			delete(a.currentBoundEqual, key)
			continue
		}
		for n := range neighbors {
			if indexBoundUpperReferencesBase(n, base) {
				delete(neighbors, n)
			}
		}
		if len(neighbors) == 0 {
			delete(a.currentBoundEqual, key)
		}
	}
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

func cloneViewMutable(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]bool, len(src))
	for name, mut := range src {
		out[name] = mut
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

// applyViewStaticLenForCondition records static-length facts for views bound by
// `if arr[a:b] is s:` conditions, so constant inner indexing inside the
// truthy block is provably in bounds. Runs inside the block's scoped fact map.
func (a *Analyzer) applyViewStaticLenForCondition(cond ast.Expr, truthy bool) {
	if a == nil || !truthy {
		return
	}
	switch n := stripOptimizationParens(cond).(type) {
	case *ast.OptionalBindExpr:
		if n.Name == "" || n.Name == "_" {
			return
		}
		a.recordViewStaticLenBinding(n.Name, n.Value, a.exprTypes[n.Value])
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND && truthy {
			a.applyViewStaticLenForCondition(n.Left, true)
			a.applyViewStaticLenForCondition(n.Right, true)
		}
	}
}

// recordViewStaticLenBinding records facts for a freshly-bound view variable
// produced by a slice: its statically-known length (for constant bounds, so
// inner constant indexing is zero-cost) and its mutability. A sliced view is
// writable iff its source is writable (and, where a frozen notion exists, not
// frozen); writing through a view of an immutable source is rejected.
func (a *Analyzer) recordViewStaticLenBinding(name string, value ast.Expr, bindingType Type) {
	if a == nil || name == "" || value == nil {
		return
	}
	inner := stripOptimizationParens(value)
	if get, ok := inner.(*ast.GetExpr); ok && get != nil {
		// `x = get arr[a:b]` carries the bounded view's facts to x.
		inner = stripOptimizationParens(get.Value)
	}
	slice, ok := inner.(*ast.SliceExpr)
	if !ok {
		// A binding to a non-slice expression clears any stale view facts.
		delete(a.currentViewStaticLen, name)
		delete(a.currentViewMutable, name)
		return
	}
	switch StripAggregateStateType(bindingType).(type) {
	case *ViewType:
	default:
		delete(a.currentViewStaticLen, name)
		delete(a.currentViewMutable, name)
		return
	}
	// A bounded view inherits the source's write capability.
	if a.currentViewMutable == nil {
		a.currentViewMutable = make(map[string]bool)
	}
	a.currentViewMutable[name] = a.sliceSourceWritable(slice.Object)
	if length, ok := a.constSliceLength(slice); ok {
		if a.currentViewStaticLen == nil {
			a.currentViewStaticLen = make(map[string]int64)
		}
		a.currentViewStaticLen[name] = length
		return
	}
	delete(a.currentViewStaticLen, name)
}

// sliceSourceWritable reports whether a slice's source permits writes through
// the resulting view. Mutability flows from the source; the frozen downgrade is
// applied only where a frozen notion exists today (packed / tree stores) — plain
// arrays and darrays carry no frozen state yet, so they pass through.
func (a *Analyzer) sliceSourceWritable(source ast.Expr) bool {
	if a == nil || source == nil {
		return false
	}
	return a.mutationPathWritable(source)
}

func (a *Analyzer) applyIndexBoundsFactsForCondition(cond ast.Expr, truthy bool) {
	a.collectBoundEqualitiesForCondition(cond, truthy)
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
	// A reassigned view binding may no longer be the slice that established its
	// static length / mutability.
	delete(a.currentViewStaticLen, base)
	delete(a.currentViewMutable, base)
	a.invalidateIndexBoundsReferencingBase(base)
	a.invalidateBoundEqualReferencingBase(base)
}

// invalidateIndexBoundsForContainer drops proofs whose upper bound refers to a
// container that was just mutated (push/extend/reserve/clear/truncate), since the
// container's count/len changed (deep audit #5).
func (a *Analyzer) invalidateIndexBoundsForContainer(container ast.Expr) {
	if a == nil || len(a.currentIndexBounds) == 0 || container == nil {
		return
	}
	base := optimizationExprString(container)
	a.invalidateIndexBoundsReferencingBase(base)
	a.invalidateBoundEqualReferencingBase(base)
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

// collectBoundEqualitiesForCondition records `A == B` equality facts from a truthy branch guard (and
// the same under conjunction). Only the truthy branch of an `==` establishes equality; a falsy `==`
// or any `!=` tells us inequality, which carries no usable upper-bound equivalence, so it is ignored.
func (a *Analyzer) collectBoundEqualitiesForCondition(cond ast.Expr, truthy bool) {
	switch n := stripOptimizationParens(cond).(type) {
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.collectBoundEqualitiesForCondition(n.Operand, !truthy)
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				a.collectBoundEqualitiesForCondition(n.Left, true)
				a.collectBoundEqualitiesForCondition(n.Right, true)
			}
		case lexer.TOKEN_EQEQ:
			if truthy {
				a.recordBoundEqual(optimizationExprString(n.Left), optimizationExprString(n.Right))
			}
		}
	}
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
		// A REFINEMENT-typed index whose proven interval [lo,hi] lies within [0, ConstSize) is in
		// bounds with no runtime check (e.g. `regs[d.sdst]`, sdst : u32 is InRange[0,127], into [128]).
		if array.HasConstSize {
			if lo, hi, ok := a.indexExprRefinementBounds(expr.Index); ok && lo >= 0 && hi < array.ConstSize {
				return true
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
	case *ArrayType, *DArrayType, *ViewType, *DStrType, *SViewType:
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
	case *ViewType, *DStrType, *SViewType:
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
