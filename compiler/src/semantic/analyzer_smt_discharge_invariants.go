package semantic

import (
	"reflect"
	"slices"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// seedSelfStructInvariantsAsAssertFacts assumes the struct invariants of every struct-typed parameter
// (e.g. `self: Box&`) as flow-assert facts at function entry — the data-structure analogue of a
// `requires` precondition. This is what lets a method `ensure result == self.model` (a refinement of
// the concrete representation onto an abstract GHOST model field) discharge: the invariant
// `self.concrete == self.model` is a well-formedness contract the caller already established, so the
// callee may assume it. Sound: a struct invariant must hold on entry to any method (it is checked at
// construction and after each field store in debug builds), so assuming it here is no stronger than the
// existing runtime contract. Dropped on mutation of the param like any other entry fact.
func (a *Analyzer) seedSelfStructInvariantsAsAssertFacts(fn *ast.FuncDecl) {
	if a == nil || fn == nil {
		return
	}
	for _, param := range a.readOnlyExpandedFuncDeclParams(fn) {
		if param.Name == "" || param.Type == nil {
			continue
		}
		st := structTypeOfParam(a.resolveType(param.Type))
		if st == nil || len(st.Invariants) == 0 {
			continue
		}
		for _, inv := range st.Invariants {
			if inv == nil {
				continue
			}
			// Rewrite `self.<field>` in the invariant onto the actual parameter name so the fact is over
			// the real binding (a no-op when the param is literally `self`).
			a.recordSMTAssertFact(rewriteSelfFieldRoot(inv, param.Name))
		}
	}
}

// structTypeOfParam unwraps a reference/generic-instance parameter type down to its underlying struct.
func structTypeOfParam(t Type) *StructType {
	switch v := t.(type) {
	case *RefType:
		return structTypeOfParam(v.Elem)
	case *StructType:
		return v
	case *GenericInstanceType:
		if st, ok := v.Base.(*StructType); ok {
			return st
		}
	}
	return nil
}

// rewriteSelfFieldRoot clones a struct-invariant expression, replacing the implicit `self` receiver
// root with `paramName`. When paramName == "self" it returns the clause unchanged.
func rewriteSelfFieldRoot(expr ast.Expr, paramName string) ast.Expr {
	if paramName == "self" || paramName == "" {
		return expr
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return &ast.Ident{Position: n.Position, Name: paramName}
		}
		return n
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: rewriteSelfFieldRoot(n.Object, paramName), Field: n.Field, Safe: n.Safe}
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: rewriteSelfFieldRoot(n.Inner, paramName)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: rewriteSelfFieldRoot(n.Left, paramName), Right: rewriteSelfFieldRoot(n.Right, paramName)}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: rewriteSelfFieldRoot(n.Operand, paramName)}
	default:
		return expr
	}
}

// smtFlowFactHypotheses asserts the scope's flow range-facts — branch-derived bounds on
// immutable variables (`if alignment == 0: return` ⟹ `alignment >= 1` afterwards; `if n < cap`
// ⟹ `n <= cap-1` in the then-branch) — as SMT hypotheses. These are already soundly
// flow-scoped and immutable-only (the linear prover uses them at the same program point), so
// surfacing them to the SMT tier lets branchy and loop-exit reasoning discharge (docs/85 gap #3:
// loop-carried/flow facts). A fact with no known bound contributes nothing.
func (a *Analyzer) smtFlowFactHypotheses(tr *smtTranslator) string {
	// Range facts flow through the unified hypothesis renderer (analyzer_fact.go). Emission stays in
	// sorted name order with closer-scope shadowing so the hypothesis text (and thus the cache key —
	// brick 5) is byte-identical to the previous hand-rolled walk.
	return a.renderHypothesisFacts(tr, collectScopeRangeFacts, true)
}

type smtFact struct {
	Expr ast.Expr
	Deps map[string]bool
	// Binders pins a `value is Variant(binders)` path fact to the exact pattern-binder symbols it
	// introduced: a binder/payload equality is emitted only while the name still resolves to that
	// symbol, never to a shadowing redeclaration that shares its SMT name.
	Binders map[string]*Symbol
	// Pins records the symbol every name in Expr resolved to when the fact was recorded. SMT symbols
	// are keyed by spelling, so a fact is rendered only while each name still resolves to its pinned
	// symbol: a later (or same-scope, later-declared) shadowing binding must not inherit it.
	Pins map[string]*Symbol
}

// smtAssertHypotheses translates flow-local proof facts into SMT assumptions. A fact may come from a
// branch guard, a proven invariant/assertion, or an exact assignment equality. Facts carry dependency
// roots and are invalidated on mutation of any root; calls still clear them conservatively.
func (a *Analyzer) smtAssertHypotheses(tr *smtTranslator) string {
	// Assert facts flow through the same unified renderer as range facts (analyzer_fact.go), with no
	// name dedupe (assert facts are emitted per-occurrence). docs/99: a closed-world scope is a wall.
	return a.renderHypothesisFacts(tr, collectScopeAssertFacts, false)
}

func (a *Analyzer) recordSMTAssertFact(expr ast.Expr) {
	if a == nil {
		return
	}
	a.recordSMTAssertFactInScope(a.currentScope, expr)
}

// recordSMTAssertFactInScope records an assumed fact into a SPECIFIC scope, not necessarily the current
// one. This matters for a branch-condition fact: it must live in the BRANCH's scope (so it is gone at
// the fall-through), but it is recorded while `currentScope` is still the parent (the branch scope is
// not made current until the block body is analyzed). Recording into `currentScope` there would leak
// the condition (e.g. `x > 10`) past the `if`, contradicting the negation the fall-through applies.
func (a *Analyzer) recordSMTAssertFactInScope(scope *Scope, expr ast.Expr) {
	if a == nil || scope == nil || expr == nil {
		return
	}
	if bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr); ok && bin.Op == lexer.TOKEN_AND {
		// Reserve once for the flattened conjunction rather than repeatedly
		// growing and copying the scope's fact list for each leaf.
		count := countSMTConjunctionFacts(expr)
		scope.smtAssertFacts = slices.Grow(scope.smtAssertFacts, count)
		a.appendSMTConjunctionFacts(scope, expr)
		return
	}
	a.appendSMTFact(scope, expr)
}

func countSMTConjunctionFacts(expr ast.Expr) int {
	if expr == nil {
		return 0
	}
	if bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr); ok && bin.Op == lexer.TOKEN_AND {
		return countSMTConjunctionFacts(bin.Left) + countSMTConjunctionFacts(bin.Right)
	}
	return 1
}

func (a *Analyzer) appendSMTConjunctionFacts(scope *Scope, expr ast.Expr) {
	if expr == nil {
		return
	}
	if bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr); ok && bin.Op == lexer.TOKEN_AND {
		a.appendSMTConjunctionFacts(scope, bin.Left)
		a.appendSMTConjunctionFacts(scope, bin.Right)
		return
	}
	a.appendSMTFact(scope, expr)
}

func (a *Analyzer) appendSMTFact(scope *Scope, expr ast.Expr) {
	deps := smtFactDeps(expr)
	scope.smtAssertFacts = append(scope.smtAssertFacts, smtFact{Expr: expr, Deps: deps, Pins: a.smtFactPinsWithDeps(scope, expr, deps)})
}

func smtFactExprForCondition(expr ast.Expr, truthy bool) ast.Expr {
	if truthy || expr == nil {
		return expr
	}
	return &ast.UnaryExpr{Position: expr.Pos(), Op: lexer.TOKEN_NOT, Operand: expr}
}

func (a *Analyzer) clearSMTAssertFacts() {
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		sc.smtAssertFacts = nil
	}
}

func (a *Analyzer) invalidateSMTAssertFactsForTarget(target ast.Expr) {
	a.invalidateSMTAssertFactsForTargetPreservingNullness(target, "")
}

// A direct reference argument passes the pointer value, not its caller-side slot.
// The callee may mutate the pointee, but cannot rebind that value. &slot and field
// arguments deliberately retain whole-target invalidation.
func (a *Analyzer) invalidateSMTAssertFactsForPointeeCall(target ast.Expr) {
	preserve := ""
	if id, ok := stripOptimizationParens(target).(*ast.Ident); ok && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(id.Name); found {
			if _, isPointer := sym.Type.(*RefType); isPointer {
				preserve = id.Name
			}
		}
	}
	a.invalidateSMTAssertFactsForTargetPreservingNullness(target, preserve)
}

func pointerNullnessFact(expr ast.Expr, name string) bool {
	expr = stripOptimizationParens(expr)
	if neg, ok := expr.(*ast.UnaryExpr); ok && neg.Op == lexer.TOKEN_NOT {
		return pointerNullnessFact(neg.Operand, name)
	}
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || (bin.Op != lexer.TOKEN_EQEQ && bin.Op != lexer.TOKEN_BANGEQ) {
		return false
	}
	id, ok := stripOptimizationParens(nullComparePointer(bin)).(*ast.Ident)
	return ok && id.Name == name
}

func (a *Analyzer) invalidateSMTAssertFactsForTargetPreservingNullness(target ast.Expr, preserve string) {
	// Drop facts depending on the target's structural root OR any place it aliases (a borrow local
	// mutated through writes the underlying place — audit cluster C). No identifiable root ⇒ clear all.
	roots := a.mutationRootsForTarget(target)
	if len(roots) == 0 {
		a.markLocalDefinitionsStale(nil)
		a.clearSMTAssertFacts()
		return
	}
	rootSet := make(map[string]bool, len(roots))
	for _, r := range roots {
		rootSet[r] = true
	}
	a.markLocalDefinitionsStale(rootSet)
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		if len(sc.smtAssertFacts) == 0 {
			continue
		}
		sc.smtAssertFacts = dropSMTFactsCopyOnWrite(sc.smtAssertFacts, func(fact smtFact) bool {
			if preserve != "" && pointerNullnessFact(fact.Expr, preserve) {
				return false
			}
			// assertHypothesisFact.deps returns this same map; avoid boxing
			// every fact merely to test its dependencies.
			return depsIntersect(fact.Deps, rootSet)
		})
	}
}

// depsIntersect reports whether any dependency root is in the given set.
func depsIntersect(deps map[string]bool, set map[string]bool) bool {
	for dep := range deps {
		if set[dep] {
			return true
		}
	}
	return false
}

func (a *Analyzer) invalidateSMTAssertFactsForCall(expr *ast.CallExpr) {
	if a == nil || expr == nil {
		return
	}
	// A resolved call carries its callee frame: drop only the facts the callee can actually falsify,
	// letting facts about provably-untouched places survive (docs/87 frame-aware fact survival).
	if ctx, ok := a.callFrameContexts[expr]; ok {
		delete(a.callFrameContexts, expr)
		a.invalidateSMTAssertFactsFramed(ctx.ft, ctx.args)
		return
	}
	// Unresolved/builtin call: conservative whole-argument invalidation.
	for _, arg := range expr.Args {
		if arg == nil {
			continue
		}
		if rt, ok := a.exprType(arg).(*RefType); ok && rt != nil && rt.Mutable {
			a.invalidateSMTAssertFactsForPointeeCall(arg)
		}
	}
}

func (a *Analyzer) recordSMTAssignmentFact(target ast.Expr, value ast.Expr) {
	if a == nil || target == nil || value == nil {
		return
	}
	targetType := a.exprType(target)
	if targetType == nil {
		if name, ok := rootIdentName(target); ok && a.currentScope != nil {
			if sym, found := a.currentScope.Lookup(name); found && sym != nil {
				targetType = sym.Type
			}
		}
	}
	valueType := a.exprType(value)
	if !isSMTExactAssignmentType(targetType) || !isSMTExactAssignmentType(valueType) {
		return
	}
	// SOUNDNESS: the fact `target == value` models target's NEW value with a single SMT symbol. If
	// `value` reads `target` itself (`y <- y - 1`), the symbol stands for both the old and the new value,
	// so the fact is self-referential and (for any non-identity update) CONTRADICTORY — a contradictory
	// hypothesis set proves every obligation. A reassignment whose RHS reads its own target cannot be
	// captured by an equality over one symbol (that is what weakest-precondition transport is for), so
	// record no fact; the prior `invalidateSMTAssertFactsForTarget` already dropped the stale ones.
	if name, ok := rootIdentName(target); ok && name != "" {
		if smtFactDeps(value)[name] {
			return
		}
	}
	value = a.implicitIntConversion(value, valueType, targetType)
	a.recordSMTAssertFact(&ast.BinaryExpr{
		Position: target.Pos(),
		Left:     target,
		Op:       lexer.TOKEN_EQEQ,
		Right:    value,
	})
}

func isSMTExactAssignmentType(t Type) bool {
	t = stripRefForBounds(t)
	if enumeration, ok := t.(*ConstEnumType); ok && enumeration != nil {
		// A const-enum value IS its integer storage value (smtNumericValueType models it so).
		t = enumeration.Storage
	}
	return t != nil && (IsNumericType(t) || IsBoolType(t)) && !IsFloatType(t)
}

func smtFactDeps(expr ast.Expr) map[string]bool {
	deps := map[string]bool{}
	collectSMTFactDeps(expr, deps)
	if len(deps) == 0 {
		return nil
	}
	return deps
}

func collectSMTFactDeps(expr ast.Expr, out map[string]bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n != nil {
			out[n.Name] = true
		}
	case *ast.ParenExpr:
		if n != nil {
			collectSMTFactDeps(n.Inner, out)
		}
	case *ast.UnaryExpr:
		if n != nil {
			collectSMTFactDeps(n.Operand, out)
		}
	case *ast.BinaryExpr:
		if n != nil {
			collectSMTFactDeps(n.Left, out)
			collectSMTFactDeps(n.Right, out)
		}
	case *ast.ExprBlock:
		bound := collectSMTBlockDeps(n.Stmts, out)
		tail := map[string]bool{}
		collectSMTFactDeps(n.Value, tail)
		for name := range tail {
			if !bound[name] {
				out[name] = true
			}
		}
	case *ast.TernaryExpr:
		collectSMTFactDeps(n.Cond, out)
		collectSMTFactDeps(n.Value, out)
		collectSMTFactDeps(n.Alt, out)
	case *ast.MatchExpr:
		collectSMTFactDeps(n.Value, out)
		collectSMTFactDeps(n.Store, out)
		for _, arm := range n.Arms {
			armDeps := map[string]bool{}
			collectSMTFactDeps(arm.Guard, armDeps)
			collectSMTStatementDeps(reflect.ValueOf(arm.Body), armDeps)
			binders := map[string]bool{}
			collectSMTMatchBinders(arm.Pattern, binders, out)
			for name := range armDeps {
				if !binders[name] {
					out[name] = true
				}
			}
		}
	case *ast.FieldExpr:
		if n != nil {
			collectSMTFactDeps(n.Object, out)
		}
	case *ast.IndexExpr:
		if n != nil {
			collectSMTFactDeps(n.Object, out)
			collectSMTFactDeps(n.Index, out)
		}
	case *ast.CallExpr:
		if n != nil {
			collectSMTFactDeps(n.Func, out)
			for _, arg := range n.Args {
				collectSMTFactDeps(arg, out)
			}
		}
	case *ast.CastExpr:
		if n != nil {
			collectSMTFactDeps(n.Operand, out)
		}
	case *ast.AddrOfExpr:
		if n != nil {
			collectSMTFactDeps(n.Operand, out)
		}
	case *ast.QuantifierExpr:
		// Collect deps from the body, then remove bound variables — they are local to the quantifier and
		// not free names. This is required so `exprReferencesResult` correctly detects a `result`
		// reference inside a quantified body (e.g. `ensure forall k: result[k] == 0` references `result`)
		// and skips the clause at void-exit where `result` is unbound.
		if n != nil {
			bodyDeps := map[string]bool{}
			collectSMTFactDeps(n.Body, bodyDeps)
			for _, v := range n.Vars {
				delete(bodyDeps, v)
			}
			for name := range bodyDeps {
				out[name] = true
			}
			collectSMTFactDeps(n.In, out)
		}
	}
}

// Separate pattern reads from binders so shadowing cannot hide a free dependency.
func collectSMTMatchBinders(pattern ast.MatchPattern, binders, reads map[string]bool) {
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		if p.Binder != "" {
			binders[p.Binder] = true
		} else {
			binders[p.Name] = true
		}
	case *ast.MatchRestPattern:
		binders[p.Name] = true
	case *ast.MatchTuplePattern:
		for _, item := range p.Elems {
			collectSMTMatchBinders(item, binders, reads)
		}
	case *ast.MatchListPattern:
		for _, item := range p.Elems {
			collectSMTMatchBinders(item, binders, reads)
		}
	case *ast.MatchOrPattern:
		for _, item := range p.Options {
			collectSMTMatchBinders(item, binders, reads)
		}
	case *ast.MatchStructPattern:
		binders[p.As] = true
		for _, arg := range p.Args {
			collectSMTMatchBinders(arg.Pattern, binders, reads)
		}
	case *ast.MatchVariantPattern:
		binders[p.As] = true
		for _, arg := range p.Args {
			collectSMTMatchBinders(arg.Pattern, binders, reads)
		}
	case *ast.MatchLiteralPattern:
		collectSMTFactDeps(p.Value, reads)
	case *ast.MatchRangePattern:
		collectSMTFactDeps(p.Lo, reads)
		collectSMTFactDeps(p.Hi, reads)
	}
}

// Walk statement structure, handing expression nodes to the dependency visitor.
// String-valued metadata, literals and declaration names are not identifier reads.
func collectSMTStatementDeps(v reflect.Value, out map[string]bool) {
	if !v.IsValid() {
		return
	}
	if v.CanInterface() {
		switch node := v.Interface().(type) {
		case []ast.Stmt:
			collectSMTBlockDeps(node, out)
			return
		case *ast.MatchStmt:
			collectSMTFactDeps(&ast.MatchExpr{Value: node.Value, Store: node.Store, Arms: node.Arms}, out)
			return
		case *ast.ForStmt:
			collectSMTFactDeps(node.Start, out)
			collectSMTFactDeps(node.End, out)
			collectSMTFactDeps(node.Step, out)
			body := map[string]bool{}
			collectSMTBlockDeps(node.Body, body)
			delete(body, node.Name)
			for name := range body {
				out[name] = true
			}
			return
		case *ast.IterForStmt:
			collectSMTFactDeps(node.Source, out)
			body := map[string]bool{}
			collectSMTBlockDeps(node.Body, body)
			for _, name := range returnBorrowIterPatternNames(node.Pattern) {
				delete(body, name)
			}
			for name := range body {
				out[name] = true
			}
			return
		}
		if expr, ok := v.Interface().(ast.Expr); ok {
			collectSMTFactDeps(expr, out)
			return
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			collectSMTStatementDeps(v.Elem(), out)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			collectSMTStatementDeps(v.Field(i), out)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectSMTStatementDeps(v.Index(i), out)
		}
	}
}

// A declaration's initializer sees the enclosing scope; its name shadows only
// subsequent statements in the same block.
func collectSMTBlockDeps(stmts []ast.Stmt, out map[string]bool) map[string]bool {
	bound := map[string]bool{}
	for _, stmt := range stmts {
		deps := map[string]bool{}
		collectSMTStatementDeps(reflect.ValueOf(stmt), deps)
		for name := range deps {
			if !bound[name] {
				out[name] = true
			}
		}
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			bound[n.Name] = true
		case *ast.TupleBindStmt:
			if n.Declare {
				for _, binding := range n.Names {
					bound[binding.Name] = true
				}
			}
		}
	}
	return bound
}
