package semantic

import (
	"elisacore/src/ast"
)

// returnBorrowFlowForValueBlock walks a value-form loop (`for x in xs |acc| -> acc:`, desugared to
// an ExprBlock): its statements write the captured outer bindings, so the block's environment
// flows back out (the trust scan relies on this). It returns what the statements return and the
// block's value.
func (a *Analyzer) returnBorrowFlowForValueBlock(block *ast.ExprBlock, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) (returnBorrowFlow, returnBorrowFlow) {
	outerScope := a.currentScope
	a.currentScope = NewScope(outerScope)
	blockEnv := a.beginReturnBorrowBranch(aliases)
	returned := a.returnBorrowFlowForStatements(block.Stmts, blockEnv, active, localBindings, false)
	var value returnBorrowFlow
	if inner, isBlock := block.Value.(*ast.ExprBlock); isBlock && inner != nil {
		// The lowered loop nests its accumulator block as the outer block's value.
		innerReturned, innerValue := a.returnBorrowFlowForValueBlock(inner, blockEnv, active, localBindings)
		returned = mergeReturnBorrowFlow(returned, innerReturned)
		value = innerValue
	} else {
		value = a.returnBorrowFlowForExpr(block.Value, blockEnv, active, localBindings)
	}
	a.currentScope = outerScope
	// The block's writes to bindings that existed before it flow back out; its own locals do not.
	a.joinReturnBorrowBranch(aliases, a.endReturnBorrowBranch(aliases), func(change returnBorrowBranchChange) bool {
		return !change.existedBefore
	})
	return returned, value
}

func (a *Analyzer) returnBorrowFlowForStatements(stmts []ast.Stmt, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool, tailValue bool) returnBorrowFlow {
	var returned returnBorrowFlow
	for index, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			if block, isBlock := n.Value.(*ast.ExprBlock); isBlock && block != nil {
				// `n: u32 = for c in xs |acc| -> acc:` is a value-form loop in value position.
				blockReturned, value := a.returnBorrowFlowForValueBlock(block, aliases, active, localBindings)
				returned = mergeReturnBorrowFlow(returned, blockReturned)
				typ := a.exprType(n.Value)
				if typ == nil && n.Type != nil {
					typ = a.returnBorrowDeclaredType(n.Type)
					value = a.returnBorrowDeclaredValueFlow(value, typ)
				}
				a.defineReturnBorrowBinding(aliases, n.Name, n, typ, value)
				continue
			}
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			flow := a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
			typ := a.exprType(n.Value)
			declaredType := typ
			if typ == nil && n.Type != nil {
				declaredType = a.returnBorrowDeclaredType(n.Type)
				typ = declaredType
				flow = a.returnBorrowDeclaredValueFlow(flow, declaredType)
			}
			if n.Type != nil && a.returnBorrowRefParamPlace(returnBorrowStripParens(n.Value)) && returnBorrowOwnedValueType(declaredType, map[Type]bool{}) && returnBorrowOwnedValueType(typ, map[Type]bool{}) {
				// `pat: P = arm.payloads[i]` copies a value out of the reference parameter's pointee:
				// it holds what the pointee holds, not the pointee's address.
				flow = a.returnBorrowReferentContentsFlow(n.Value, aliases, active, localBindings)
			}
			a.defineReturnBorrowBinding(aliases, n.Name, n, typ, flow)
		case *ast.AssignStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Target, aliases, active, localBindings)
			if ident, ok := n.Target.(*ast.Ident); ok {
				a.setReturnBorrowAlias(aliases, ident.Name, a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings), true)
			} else if root := returnBorrowPlaceRootName(n.Target); root != "" {
				// A store into a field/element of a local (`h.r <- &x`, `xs[0] <- &x`) makes the
				// whole local carry the stored value's borrows.
				a.setReturnBorrowAlias(aliases, root, a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings), true)
			}
		case *ast.AugAssignStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Target, aliases, active, localBindings)
			if n.CollectionAppend != nil {
				a.noteReturnBorrowCallArgumentStores(n.CollectionAppend, aliases, active, localBindings)
			}
			if root := returnBorrowPlaceRootName(n.Target); root != "" {
				a.setReturnBorrowAlias(aliases, root, a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings), true)
			}
		case *ast.TupleBindStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			flow := a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
			for _, name := range n.Names {
				if n.Declare {
					a.defineReturnBorrowBinding(aliases, name.Name, n, nil, flow)
				} else if name.Name != "" && name.Name != "_" {
					a.setReturnBorrowAlias(aliases, name.Name, flow, true)
				}
			}
		case *ast.IterForStmt:
			a.noteReturnBorrowCallArgumentStores(n.Source, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.WhereFilter, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Filter, aliases, active, localBindings)
			// A reference binding holds an address into the iterated storage; a value binding
			// holds what the source's elements hold.
			var binderFlow returnBorrowFlow
			var binderType Type
			switch {
			case n.Mode != ast.IterBindValue:
				binderFlow = a.returnBorrowAddressFlow(n.Source, aliases, active, localBindings)
				binderType = &RefType{Mutable: n.Mode == ast.IterBindMutableRef}
			default:
				binderFlow = a.returnBorrowIterValueFlow(n.Source, aliases, active, localBindings)
			}
			loopEnv := a.beginReturnBorrowBranch(aliases)
			outerScope := a.currentScope
			a.currentScope = NewScope(outerScope)
			names := returnBorrowIterPatternNames(n.Pattern)
			for _, name := range names {
				a.defineReturnBorrowBinding(loopEnv, name, n.Pattern, binderType, binderFlow)
			}
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Body, loopEnv, active, localBindings, false))
			a.currentScope = outerScope
			// The loop binders are the loop's own: an outer binding of the same name keeps its value.
			binderNames := map[string]bool{}
			for _, name := range names {
				binderNames[name] = true
			}
			a.joinReturnBorrowBranch(aliases, a.endReturnBorrowBranch(aliases), func(change returnBorrowBranchChange) bool {
				return binderNames[change.name]
			})
		case *ast.ReturnStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			returnType := a.returnBorrowWalkReturnType()
			if n.Value != nil && a.returnBorrowRefParamPlace(returnBorrowStripParens(n.Value)) && returnBorrowOwnedValueType(returnType, map[Type]bool{}) {
				// `return p` from `p: lmut P` / `p: P&` with `-> P` copies the pointee out: the
				// result holds what the referent holds (Contents), never the caller's address.
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowReferentContentsFlow(n.Value, aliases, active, localBindings))
				continue
			}
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForCopiedValue(n.Value, returnType, aliases, active, localBindings))
		case *ast.ExprStmt:
			if block, isBlock := n.Expr.(*ast.ExprBlock); isBlock && block != nil {
				blockReturned, value := a.returnBorrowFlowForValueBlock(block, aliases, active, localBindings)
				returned = mergeReturnBorrowFlow(returned, blockReturned)
				if tailValue && index+1 == len(stmts) {
					returned = mergeReturnBorrowFlow(returned, value)
				}
				continue
			}
			a.noteReturnBorrowCallArgumentStores(n.Expr, aliases, active, localBindings)
			if tailValue && index+1 == len(stmts) {
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForExpr(n.Expr, aliases, active, localBindings))
			}
		case *ast.DiscardStmt:
			if call := a.returnBorrowDiscardedCall(n); call != nil {
				a.noteReturnBorrowCallArgumentStores(call, aliases, active, localBindings)
			}
		case *ast.IfStmt:
			a.noteReturnBorrowConditionStores(n.Cond, aliases, active, localBindings)
			for _, clause := range n.Elifs {
				a.noteReturnBorrowConditionStores(clause.Cond, aliases, active, localBindings)
			}
			branchChanges := make([][]returnBorrowBranchChange, 0, len(n.Elifs)+2)
			outerScope := a.currentScope
			thenEnv := a.beginReturnBorrowBranch(aliases)
			a.currentScope = NewScope(outerScope)
			a.defineReturnBorrowConditionBindings(n.Cond, thenEnv, active, localBindings)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Then, thenEnv, active, localBindings, false))
			branchChanges = append(branchChanges, a.endReturnBorrowBranch(aliases))
			for _, clause := range n.Elifs {
				elifEnv := a.beginReturnBorrowBranch(aliases)
				a.currentScope = NewScope(outerScope)
				a.defineReturnBorrowConditionBindings(clause.Cond, elifEnv, active, localBindings)
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(clause.Body, elifEnv, active, localBindings, false))
				branchChanges = append(branchChanges, a.endReturnBorrowBranch(aliases))
			}
			elseEnv := a.beginReturnBorrowBranch(aliases)
			a.currentScope = NewScope(outerScope)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Else, elseEnv, active, localBindings, false))
			a.currentScope = outerScope
			branchChanges = append(branchChanges, a.endReturnBorrowBranch(aliases))
			for _, changes := range branchChanges {
				a.joinReturnBorrowBranch(aliases, changes, nil)
			}
		case *ast.WhileStmt:
			a.noteReturnBorrowCallArgumentStores(n.Cond, aliases, active, localBindings)
			loopEnv := a.beginReturnBorrowBranch(aliases)
			outerScope := a.currentScope
			a.currentScope = NewScope(outerScope)
			// `while current is v`: v is rebound from the subject on every iteration; a read of the
			// subject joins every store into it, so the binding covers each iteration's value.
			a.defineReturnBorrowConditionBindings(n.Cond, loopEnv, active, localBindings)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Body, loopEnv, active, localBindings, false))
			a.currentScope = outerScope
			a.joinReturnBorrowBranch(aliases, a.endReturnBorrowBranch(aliases), nil)
		case *ast.ForStmt:
			a.noteReturnBorrowCallArgumentStores(n.Start, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.End, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Step, aliases, active, localBindings)
			loopEnv := a.beginReturnBorrowBranch(aliases)
			outerScope := a.currentScope
			a.currentScope = NewScope(outerScope)
			returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(n.Body, loopEnv, active, localBindings, false))
			a.currentScope = outerScope
			a.joinReturnBorrowBranch(aliases, a.endReturnBorrowBranch(aliases), nil)
		case *ast.CanStmt, *ast.ScopeStmt, *ast.RegionStmt, *ast.InStoreStmt, *ast.PoolStmt, *ast.LockStmt,
			*ast.CheckpointStmt, *ast.GroupedCheckpointStmt, *ast.StaticBlockStmt,
			*ast.ParallelForStmt, *ast.StaticIfStmt:
			// Block statements: a `return` nested in `can ...:`, `in r:`, a lock, an iterator loop,
			// etc. is a return of the function all the same. Without these arms every aggregate
			// return inside an effect block escaped the check (`can Abort.Panic: return H{r: &x}`).
			// A static-if's branches are alternatives: each starts from the environment before it.
			var blockChanges [][]returnBorrowBranchChange
			for _, body := range returnBorrowChildBlocks(n) {
				blockEnv := a.beginReturnBorrowBranch(aliases)
				outerScope := a.currentScope
				a.currentScope = NewScope(outerScope)
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(body, blockEnv, active, localBindings, false))
				a.currentScope = outerScope
				blockChanges = append(blockChanges, a.endReturnBorrowBranch(aliases))
			}
			for _, changes := range blockChanges {
				a.joinReturnBorrowBranch(aliases, changes, nil)
			}
		case *ast.MatchStmt:
			a.noteReturnBorrowCallArgumentStores(n.Value, aliases, active, localBindings)
			a.noteReturnBorrowCallArgumentStores(n.Store, aliases, active, localBindings)
			binders := make([][]returnBorrowMatchBinder, len(n.Arms))
			var subjectFlow, subjectViewFlow returnBorrowFlow
			for index, arm := range n.Arms {
				binders[index] = returnBorrowMatchPatternBinders(arm.Pattern, nil)
				for _, binder := range binders[index] {
					if binder.view {
						subjectViewFlow = a.returnBorrowAddressFlow(n.Value, aliases, active, localBindings)
					}
				}
			}
			if n.Value != nil {
				subjectFlow = a.returnBorrowFlowForExpr(n.Value, aliases, active, localBindings)
			}
			// Every arm starts from the environment before the match; their writes join afterwards.
			armChanges := make([][]returnBorrowBranchChange, 0, len(n.Arms))
			for index, arm := range n.Arms {
				armEnv := a.beginReturnBorrowBranch(aliases)
				outerScope := a.currentScope
				a.currentScope = NewScope(outerScope)
				for _, binder := range binders[index] {
					flow := subjectFlow
					if binder.view {
						flow = mergeReturnBorrowFlow(flow, subjectViewFlow)
					}
					a.defineReturnBorrowBinding(armEnv, binder.name, binder.node, nil, flow)
				}
				a.noteReturnBorrowCallArgumentStores(arm.Guard, armEnv, active, localBindings)
				returned = mergeReturnBorrowFlow(returned, a.returnBorrowFlowForStatements(arm.Body, armEnv, active, localBindings, false))
				a.currentScope = outerScope
				armChanges = append(armChanges, a.endReturnBorrowBranch(aliases))
			}
			for _, changes := range armChanges {
				a.joinReturnBorrowBranch(aliases, changes, nil)
			}
		}
	}
	return returned
}

func cloneReturnBorrowAliases(src map[string]returnBorrowFlow) map[string]returnBorrowFlow {
	cloned := make(map[string]returnBorrowFlow, len(src))
	for name, flow := range src {
		cloned[name] = flow
	}
	return cloned
}

func (a *Analyzer) mergeReturnBorrowAliasMaps(dst, src map[string]returnBorrowFlow) {
	if dst == nil {
		// The caller keeps no alias environment (a value walked outside a body).
		return
	}
	for name, flow := range src {
		a.writeReturnBorrowAlias(dst, name, mergeReturnBorrowFlow(dst[name], flow))
	}
}

func lastDotIndex(value string) int {
	for index := len(value) - 1; index >= 0; index-- {
		if value[index] == '.' {
			return index
		}
	}
	return -1
}

// returnBorrowChildBlocks lists the nested bodies of the block statements that
// returnBorrowFlowForStatements does not special-case (If/While/For/Match have their own arms).
func returnBorrowChildBlocks(stmt ast.Stmt) [][]ast.Stmt {
	switch s := stmt.(type) {
	case *ast.CanStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.ScopeStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.RegionStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.InStoreStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.PoolStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.LockStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.CheckpointStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.GroupedCheckpointStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.StaticBlockStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.IterForStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.ParallelForStmt:
		return [][]ast.Stmt{s.Body}
	case *ast.StaticIfStmt:
		out := [][]ast.Stmt{s.Then}
		for _, clause := range s.Elifs {
			out = append(out, clause.Body)
		}
		return append(out, s.Else)
	}
	return nil
}

// returnBorrowPlaceRootName returns the local identifier a field/index place is rooted at
// (`h.r` -> "h", `xs[i].r` -> "xs"), or "" for a non-place.
func returnBorrowPlaceRootName(expr ast.Expr) string {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			expr = n.Object
		case *ast.IndexExpr:
			expr = n.Object
		case *ast.Ident:
			return n.Name
		default:
			return ""
		}
	}
}

// returnBorrowContainerInsertMethods are the container methods that retain their argument in the
// receiver's storage.
var returnBorrowContainerInsertMethods = map[string]bool{
	"push": true, "extend": true, "insert": true, "put": true, "add": true, "append": true,
	"push_back": true, "push_front": true, "set": true,
}

// noteReturnBorrowCallArgumentStores records the stores a call may make into its arguments:
//   - a call with a known signature may store its other arguments through each writable parameter
//     (`mutable T&`, `lmut`, `mutable view[T]`) whose referent can hold a borrow: `add(out, &x)`
//     with `xs: mutable darray[i64&]&` makes `out` carry `&x`'s borrows, as `out.push(&x)` does. A
//     reference parameter receives the argument's ADDRESS (`add(out, x)` with `r: i64&` borrows x);
//   - a builtin method may store its arguments into its receiver (`xs.push(&x)`) unless it only
//     reads;
//   - any other builtin may store its other arguments through an address or writable reference.
//
// Every call it visits is marked processed for the store environment's trust scan.
func (a *Analyzer) noteReturnBorrowCallArgumentStores(expr ast.Expr, aliases map[string]returnBorrowFlow, active map[*ast.FuncDecl]bool, localBindings map[*Symbol]bool) {
	if expr == nil {
		return
	}
	a.walkStaticExpr(expr, func(e ast.Expr) bool {
		// A binding expression is walked with its binders in scope.
		switch n := e.(type) {
		case *ast.MatchExpr:
			a.returnBorrowFlowForMatchExpr(n, aliases, active, localBindings)
			return true
		case *ast.ListComprehensionExpr:
			a.returnBorrowFlowForComprehension(n, aliases, active, localBindings)
			return true
		case *ast.QueryExpr:
			if n.Pattern == nil {
				a.returnBorrowFlowForQuery(n, aliases, active, localBindings)
				return true
			}
		case *ast.TernaryExpr:
			if returnBorrowMarkConditionBinders(n.Cond, nil) {
				env := a.beginReturnBorrowBranch(aliases)
				outerScope := a.currentScope
				a.currentScope = NewScope(outerScope)
				a.noteReturnBorrowAndChainStores(n.Cond, env, active, localBindings)
				a.defineReturnBorrowConditionBindings(n.Cond, env, active, localBindings)
				a.noteReturnBorrowCallArgumentStores(n.Value, env, active, localBindings)
				a.endReturnBorrowBranch(aliases)
				a.currentScope = outerScope
				a.noteReturnBorrowCallArgumentStores(n.Alt, aliases, active, localBindings)
				return true
			}
		}
		call, isCall := e.(*ast.CallExpr)
		if lit, isLit := e.(*ast.StructLitExpr); isLit && lit != nil {
			// Parens are walked separately: only the node itself is a call here.
			call, isCall = a.returnBorrowAsCall(lit), true
		}
		if !isCall || call == nil {
			return false
		}
		if a.returnBorrowRecordingActive() {
			a.returnBorrowDeclProcessedCalls[call] = true
		}
		args := returnBorrowCallArgs(call)
		otherArgsFlow := func(skip int, params []Type) returnBorrowFlow {
			return a.returnBorrowCallOtherArgsFlow(args, skip, params, aliases, active, localBindings)
		}
		if fnType := a.returnBorrowCalleeSignature(call); fnType != nil {
			for index, paramType := range fnType.Params {
				if index >= len(args) {
					break
				}
				if !a.returnBorrowWritableParam(paramType) {
					continue
				}
				root := returnBorrowPlaceRootName(returnBorrowStripAddr(args[index]))
				if root == "" {
					continue
				}
				// The same bound the caller-side store check applies: a summarized callee
				// states which arguments reach this parameter, and with what of them.
				keep, contentsOnly := a.returnBorrowCallStorePredicates(call, index)
				if flow := a.returnBorrowCallOtherArgsFlowWhereKinds(args, index, fnType.Params, keep, contentsOnly, aliases, active, localBindings); !flow.empty() {
					a.setReturnBorrowAlias(aliases, root, flow, true)
				}
			}
			return false
		}
		if field, isField := call.Func.(*ast.FieldExpr); isField && field != nil && !returnBorrowReadOnlyMethods[field.Field] {
			if root := returnBorrowPlaceRootName(field.Object); root != "" {
				if flow := otherArgsFlow(-1, nil); !flow.empty() {
					a.setReturnBorrowAlias(aliases, root, flow, true)
				}
			}
		}
		for index, arg := range args {
			_, isAddr := returnBorrowStripParens(arg).(*ast.AddrOfExpr)
			ref, isRef := a.exprType(arg).(*RefType)
			if !isAddr && !(isRef && ref != nil && ref.Mutable) {
				continue
			}
			root := returnBorrowPlaceRootName(returnBorrowStripAddr(arg))
			if root == "" {
				continue
			}
			if flow := otherArgsFlow(index, nil); !flow.empty() {
				a.setReturnBorrowAlias(aliases, root, flow, true)
			}
		}
		return false
	})
}
