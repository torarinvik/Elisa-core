package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// sviewFrameWriteFree reports whether decl's body can never write memory, so the bytes behind an
// immutable sview parameter are the same at every point of the function. Then the parameter can be
// modeled as ONE byte array for the whole body (contents and length fixed), which is what lets a
// call-initialized local `s: usize = f(text, 0)` and a contract call `f(text, 0)` share a canonical
// call symbol (call congruence keys a view by its array term).
//
// The gate is syntactic over the whole body (statements after the proof point included):
//   - locals: immutable bindings of write-free values; MUTABLE bindings only when the declared
//     type is a scalar (integer/bool), sview, or a plain scalar-only record —
//     rebinding such a slot writes no shared memory;
//   - assignments `x <- v` / `x += v` only to those mutable scalar/view locals;
//   - if/elif/else, while (condition and body write-free), return, read-only contracts, and
//     Abort.Panic and selected Unsafe permission blocks, with their bodies still
//     checked for writes and unknown calls (a grant alone performs no operation);
//   - expressions: identifiers, literals, operators without overloads, ternaries, non-optional
//     field reads, view slices and indexing, plain integer casts, tuples, noncapturing
//     value blocks checked under these same rules, and calls to callees
//     classified write-free, or completed non-generic bodies checked by this gate;
//   - the function has no mutable parameters and uses only those inspectable permissions.
//
// Anything else declines (the caller keeps the conservative per-read modeling).
func (a *Analyzer) sviewFrameWriteFree(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || decl.IsLemma || len(decl.Body) == 0 {
		return false
	}
	if cached, ok := a.sviewFrameWriteFreeCache[decl]; ok {
		return cached
	}
	if a.sviewFrameWriteFreeCache == nil {
		a.sviewFrameWriteFreeCache = map[*ast.FuncDecl]bool{}
	}
	// Decline recursive call cycles while inspecting their bodies. A recursive
	// callee must never make a partially checked frame look write-free.
	a.sviewFrameWriteFreeCache[decl] = false
	result := a.computeSViewFrameWriteFree(decl)
	a.sviewFrameWriteFreeCache[decl] = result
	return result
}

func (a *Analyzer) computeSViewFrameWriteFree(decl *ast.FuncDecl) bool {
	for _, param := range decl.Params {
		if param.Mutable || a.paramIsMutable(param) {
			return false
		}
	}
	readOnly := func(permissions []ast.PermissionRef) bool {
		for _, permission := range permissions {
			if !sviewFrameInspectablePermission(permission) {
				return false
			}
		}
		return true
	}
	if !readOnly(decl.Permissions) {
		return false
	}
	if decl == a.currentFuncDecl && !readOnly(a.currentFunctionUsedPermissionRefs) {
		return false
	}
	g := &sviewFrameGate{a: a, decl: decl, rebindable: map[string]bool{}, types: map[string]Type{}}
	sym, _ := a.symbolForFuncDecl(decl)
	var fn *FuncType
	if sym != nil {
		fn, _ = sym.Type.(*FuncType)
	}
	for i, param := range decl.Params {
		g.rebindable[param.Name] = false
		g.types[param.Name] = nil
		if fn != nil && i < len(fn.Params) {
			g.types[param.Name] = fn.Params[i]
		}
	}
	return g.stmts(decl.Body)
}

type sviewFrameGate struct {
	a    *Analyzer
	decl *ast.FuncDecl
	// rebindable records parameters and locals, with true only for safe local
	// assignments. Keeping immutable names also preserves builtin shadowing.
	rebindable map[string]bool
	types      map[string]Type
}

// These capabilities may enclose read-only code. They do not classify an
// operation or callee as read-only: every body, assignment and call must still
// pass the structural gate. In particular RawExtern never admits an extern call.
func sviewFrameInspectablePermission(permission ast.PermissionRef) bool {
	if len(permission.TypeArgs) != 0 || len(permission.Via) != 0 {
		return false
	}
	return (permission.Name == "Abort" && permission.Member == "Panic") ||
		(permission.Name == "Unsafe" && (permission.Member == "UncheckedIndex" || permission.Member == "AssumeProgress" ||
			permission.Member == "PointerArithmetic" || permission.Member == "PointerCast" || permission.Member == "RawExtern"))
}

func sviewFrameRebindableType(te ast.TypeExpr) bool {
	named, ok := te.(*ast.NamedType)
	if !ok || named == nil || named.Region != "" {
		return false
	}
	switch named.Name {
	case "bool", "sview", "char":
		return true
	}
	return isIntegerConversionTargetName(named.Name)
}

func (g *sviewFrameGate) stmts(body []ast.Stmt) bool {
	for _, stmt := range body {
		if !g.stmt(stmt) {
			return false
		}
	}
	return true
}

func (g *sviewFrameGate) stmt(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return n.Value == nil || g.expr(n.Value, 0)
	case *ast.VarDeclStmt:
		if n == nil || n.Owner != nil || n.Value == nil || n.Ghost || !g.expr(n.Value, 0) {
			return false
		}
		typ := g.receiverType(n.Value, 0)
		if named, ok := n.Type.(*ast.NamedType); ok && named != nil {
			if declared, _, found := g.a.lookupVisibleType(named.Name); found {
				typ = declared
			}
		}
		g.types[n.Name] = typ
		if n.Mutable {
			if !sviewFrameRebindableType(n.Type) && !g.plainRecordLocalType(n.Type) {
				// Runtime quantifiers introduce inferred mutable Boolean temps.
				_, boolean := stripOptimizationParens(n.Value).(*ast.BoolLit)
				if n.Type != nil || !boolean {
					return false
				}
			}
			g.rebindable[n.Name] = true
		} else {
			// An immutable binding shadows any same-named rebindable local.
			g.rebindable[n.Name] = false
		}
		return true
	case *ast.AssignStmt:
		if n == nil || n.Optional || n.AsOverlayCall != nil {
			return false
		}
		id, ok := n.Target.(*ast.Ident)
		return ok && id != nil && g.rebindable[id.Name] && g.expr(n.Value, 0)
	case *ast.AugAssignStmt:
		if n == nil || n.CollectionAppend != nil {
			return false
		}
		id, ok := n.Target.(*ast.Ident)
		return ok && id != nil && g.rebindable[id.Name] && g.expr(n.Value, 0)
	case *ast.IfStmt:
		if n == nil || !g.expr(n.Cond, 0) || !g.scoped(n.Then) || !g.scoped(n.Else) {
			return false
		}
		for _, clause := range n.Elifs {
			if !g.expr(clause.Cond, 0) || !g.scoped(clause.Body) {
				return false
			}
		}
		return true
	case *ast.WhileStmt:
		return n != nil && g.expr(n.Cond, 0) && g.scoped(n.Body)
	case *ast.ForStmt:
		if n == nil || ast.HasSynthesizedPreReserveStmts(n) || !g.expr(n.Start, 0) || !g.expr(n.End, 0) ||
			(n.Step != nil && !g.expr(n.Step, 0)) {
			return false
		}
		outer, bound := g.rebindable[n.Name]
		outerType := g.types[n.Name]
		g.types[n.Name] = g.receiverType(n.Start, 0)
		g.rebindable[n.Name] = false
		ok := g.scoped(n.Body)
		if bound {
			g.rebindable[n.Name] = outer
			g.types[n.Name] = outerType
		} else {
			delete(g.rebindable, n.Name)
			delete(g.types, n.Name)
		}
		return ok
	case *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.ExprStmt:
		call, ok := n.Expr.(*ast.CallExpr)
		if !ok || call.Safe || call.SafeReceiver != nil || len(call.Args) != 1 {
			return false
		}
		if g.a.isCompilerBuiltinHelperCall(call, "assert") || g.a.isCompilerBuiltinHelperCall(call, "ASSERT") {
			return g.expr(call.Args[0], 0)
		}
		if g.a.exprTypes[call.Func] != nil {
			// An analyzed user call must not be reclassified by its spelling.
			return false
		}
		id, ok := call.Func.(*ast.Ident)
		if !ok {
			return false
		}
		if _, shadowed := g.rebindable[id.Name]; shadowed {
			return false
		}
		// Whole-frame inspection can reach assertions not analyzed yet. Resolve
		// those against the current scope and the lexical locals tracked above.
		condition, builtin := g.a.assertedCondition(n.Expr)
		return builtin && g.expr(condition, 0)
	case *ast.ContractStmt:
		// Loop `invariant` / `decreases` clauses only read (they are checked in place).
		if n == nil {
			return false
		}
		switch n.Kind {
		case ast.ContractRequire, ast.ContractEnsure, ast.ContractInvariant, ast.ContractDecreases:
			return n.Cond != nil && g.expr(n.Cond, 0)
		case ast.ContractDecreasesWild:
			return true
		}
		return false
	case *ast.AssertByStmt:
		// The proof block is verification-only; the condition is checked in place.
		return n != nil && g.expr(n.Cond, 0)
	case *ast.RegionStmt:
		// A compiler-generated lazy wrapper owns no storage until its body
		// allocates. Inspect that body; explicit/user regions stay outside the
		// frame model, as do all allocation calls and writes within a wrapper.
		return isCompilerGeneratedAutoRegionStmt(n) &&
			n.Capacity == nil && n.OwnerName == "" && n.Allocator == "" && g.scoped(n.Body)
	case *ast.CanStmt:
		if n == nil || n.HandlerName != "" || len(n.HandlerArgs) != 0 || n.As != "" || len(n.Permissions) == 0 {
			return false
		}
		for _, permission := range n.Permissions {
			if !sviewFrameInspectablePermission(permission) {
				return false
			}
		}
		return g.scoped(n.Body)
	}
	return false
}

// scoped checks a nested block; its declarations go out of scope at the end.
func (g *sviewFrameGate) scoped(body []ast.Stmt) bool {
	savedTypes := g.cloneTypes()
	defer func() { g.types = savedTypes }()
	saved := make(map[string]bool, len(g.rebindable))
	for k, v := range g.rebindable {
		saved[k] = v
	}
	ok := g.stmts(body)
	g.rebindable = saved
	return ok
}

func (g *sviewFrameGate) expr(e ast.Expr, depth int) bool {
	if e == nil || depth > 64 {
		return false
	}
	switch n := e.(type) {
	case *ast.Ident, *ast.StringLit, *ast.BoolLit, *ast.IntLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return g.expr(n.Inner, depth+1)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && g.binaryHasNoOverload(n) && g.expr(n.Left, depth+1) && g.expr(n.Right, depth+1)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && (n.Op == lexer.TOKEN_NOT || (n.Op == lexer.TOKEN_MINUS && g.unaryHasNoOverload(n))) && g.expr(n.Operand, depth+1)
	case *ast.TernaryExpr:
		return g.expr(n.Cond, depth+1) && g.expr(n.Value, depth+1) && g.expr(n.Alt, depth+1)
	case *ast.FieldExpr:
		return !n.Safe && g.expr(n.Object, depth+1)
	case *ast.SliceExpr:
		return n.Start != nil && n.End != nil && g.expr(n.Object, depth+1) && g.expr(n.Start, depth+1) && g.expr(n.End, depth+1)
	case *ast.IndexExpr:
		return n.Index2 == nil && n.Fallback == nil && n.AsOverlayCall == nil && g.builtinIndex(n.Object) && g.expr(n.Object, depth+1) && g.expr(n.Index, depth+1)
	case *ast.CastExpr:
		return isPureNumericValueCast(n) && IsNumericType(valueContextOperandType(g.receiverType(n.Operand, 0))) && g.expr(n.Operand, depth+1)
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if !g.expr(elem, depth+1) {
				return false
			}
		}
		return len(n.Elems) != 0
	case *ast.StructLitExpr:
		if !n.Brace || n.CopyBase != nil || len(n.Spreads) != 0 || len(n.TypeArgs) != 0 {
			return false
		}
		typ, _, found := g.a.lookupVisibleType(n.Name)
		if !found || (!plainFrameRecordType(typ, map[*StructType]bool{}) && !sviewFrameHeaderType(typ)) {
			return false
		}
		st := StripAggregateStateType(typ).(*StructType)
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		} else if len(args) != len(st.Fields) {
			// Missing fields may introduce default expressions not analyzed yet.
			return false
		}
		for _, arg := range args {
			if !g.expr(arg, depth+1) {
				return false
			}
		}
		return true
	case *ast.ExprBlock:
		if len(n.Captures) != 0 || n.Value == nil {
			return false
		}
		saved := g.rebindable
		savedTypes := g.cloneTypes()
		g.rebindable = make(map[string]bool, len(saved))
		for name, rebindable := range saved {
			g.rebindable[name] = rebindable
		}
		defer func() { g.rebindable = saved; g.types = savedTypes }()
		return g.stmts(n.Stmts) && g.expr(n.Value, depth+1)
	case *ast.QuantifierExpr:
		return g.quantifier(n, depth)
	case *ast.MembershipRangeExpr:
		return g.expr(n.Start, depth+1) && g.expr(n.End, depth+1)
	case *ast.MatchExpr:
		if n.Store != nil || !g.expr(n.Value, depth+1) {
			return false
		}
		for _, arm := range n.Arms {
			if !sviewFramePattern(arm.Pattern, 0) {
				return false
			}
			if arm.Guard != nil && !g.expr(arm.Guard, depth+1) {
				return false
			}
			if len(arm.Body) != 1 {
				return false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !g.expr(tail.Expr, depth+1) {
				return false
			}
		}
		return true
	case *ast.CallExpr:
		if id, ok := n.Func.(*ast.Ident); ok {
			if _, local := g.rebindable[id.Name]; local {
				// A callback can shadow a read-only namespace function.
				return false
			}
		}
		callee, ok := g.a.classifierFrameCallDecl(n)
		// A default/forwarded argument can execute code absent from n.Args.
		// Apply this guard before every callee classification, including pure
		// equations: purity of the body says nothing about argument evaluation.
		if !ok || callee == nil || callee.Variadic || n.HasArgForward || len(n.Args) != len(callee.Params) || !(g.a.functionPureEquationShape(callee) || g.a.classifierCalleeWriteFree(callee) || g.a.sviewFrameCallBodyWriteFree(callee, len(n.Args))) {
			return false
		}
		for _, arg := range n.Args {
			if !g.expr(arg, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

func (g *sviewFrameGate) plainRecordLocalType(expr ast.TypeExpr) bool {
	named, ok := expr.(*ast.NamedType)
	if !ok || named == nil || named.Region != "" {
		return false
	}
	typ, _, found := g.a.lookupVisibleType(named.Name)
	return found && plainFrameRecordType(typ, map[*StructType]bool{})
}

// Rebinding a plain record copies only scalar values. References, containers,
// resources and destructors are excluded: they could write shared memory.
func plainFrameRecordType(typ Type, active map[*StructType]bool) bool {
	st, ok := StripAggregateStateType(typ).(*StructType)
	if !ok || st == nil || st.Affine || st.Resource || st.DropHook != "" || active[st] {
		return false
	}
	active[st] = true
	defer delete(active, st)
	for _, field := range st.Fields {
		if IsBoolType(field.Type) {
			continue
		}
		if _, _, integer := smtIntWidthSign(field.Type); integer {
			continue
		}
		if !plainFrameRecordType(field.Type, active) {
			return false
		}
	}
	return true
}

// sviewFramePattern accepts patterns whose test evaluates nothing: wildcards, string/int/bool/char
// literals (never pinned: a pinned pattern evaluates its expression), literal ranges, and
// or-patterns of those.
func sviewFramePattern(pattern ast.MatchPattern, depth int) bool {
	if depth > 16 {
		return false
	}
	if pureClassifierPattern(pattern) {
		return true
	}
	switch n := pattern.(type) {
	case *ast.MatchLiteralPattern:
		if n == nil || n.Pinned {
			return false
		}
		_, ok := stripOptimizationParens(n.Value).(*ast.CharLit)
		return ok
	case *ast.MatchRangePattern:
		if n == nil {
			return false
		}
		literal := func(e ast.Expr) bool {
			switch stripOptimizationParens(e).(type) {
			case *ast.IntLit, *ast.CharLit:
				return true
			}
			return false
		}
		return literal(n.Lo) && literal(n.Hi)
	case *ast.MatchOrPattern:
		if n == nil || len(n.Options) == 0 {
			return false
		}
		for _, option := range n.Options {
			if !sviewFramePattern(option, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// A completed, non-generic body can prove that a table/reference helper only
// reads memory even though its parameter types exceed the scalar classifier.
// Require every argument explicitly: an unexpanded default can execute a call.
func (a *Analyzer) sviewFrameCallBodyWriteFree(decl *ast.FuncDecl, argCount int) bool {
	if decl == nil || decl.Variadic || argCount != len(decl.Params) || len(decl.TypeParams) != 0 ||
		len(decl.GenericParams) != 0 || len(decl.RegionParams) != 0 || len(decl.PermissionParams) != 0 {
		return false
	}
	sym, found := a.symbolForFuncDecl(decl)
	if !found || sym == nil {
		return false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil || !fn.ReturnBorrowedOwnerRefsKnown ||
		len(fn.MutableGlobalPermissionRefs) != 0 || len(fn.MutableGlobalSourceNames) != 0 {
		return false
	}
	for _, permission := range fn.PermissionRefs {
		if !sviewFrameInspectablePermission(permission) {
			return false
		}
	}
	return a.sviewFrameWriteFree(decl)
}
