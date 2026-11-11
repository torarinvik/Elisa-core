package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// fillMayAdopt reports whether a function may put data allocated in a caller's arena into a
// container parameter. Only a void grower allocates there (its ambient region is the grown
// container's), so a function may do so only if it can reach one through calls: this is the
// syntactic over-approximation used for callees whose body summary (ambientFillAnalyzed /
// ambientFillFresh) is not available yet — declared later, or recursive and still in progress.
func (a *Analyzer) fillMayAdopt(decl *ast.FuncDecl) bool {
	if a.fillMayAdoptSet == nil {
		a.fillMayAdoptSet = a.computeFillMayAdopt()
	}
	adopt, known := a.fillMayAdoptSet[decl]
	return adopt || !known
}

// computeFillMayAdopt builds the call graph over every declared function by name (all overloads,
// methods by their last name segment) and closes the void-grower seeds backwards over it. A call
// whose target cannot be named from the syntax — through a parameter, local, global or field
// holding a function value, or any computed callee — is a seed too.
func (a *Analyzer) computeFillMayAdopt() map[*ast.FuncDecl]bool {
	byName := map[string][]*ast.FuncDecl{}
	for decl := range a.funcDeclSymbols {
		if decl != nil {
			name := fillMayAdoptLastSegment(decl.Name)
			byName[name] = append(byName[name], decl)
		}
	}
	structNames, funcFields := fillMayAdoptStructFacts(a.file)
	result := map[*ast.FuncDecl]bool{}
	callers := map[*ast.FuncDecl][]*ast.FuncDecl{}
	firstParamHeads := map[*ast.FuncDecl]string{}
	var work []*ast.FuncDecl
	seed := func(decl *ast.FuncDecl) {
		if !result[decl] {
			result[decl] = true
			work = append(work, decl)
		}
	}
	var bodyNodes []any
	for decl := range a.funcDeclSymbols {
		if decl == nil {
			continue
		}
		result[decl] = false
		// A void grower is a seed only if its body can put freshly allocated data (data in the
		// adopted arena) into its container: a construction, an allocating call, or a call to a
		// region-polymorphic callee. One that only moves argument-derived values around adopts
		// nothing, and its callers' fills stay tracked. Calls into other seeds close over callers.
		if decl.AmbientGrownContainerRegion != "" && a.fillMayAdoptFreshShapes(decl, byName) {
			seed(decl)
			// A seed is already in the closure regardless of its callees.
			continue
		}
		valueParams := map[string]bool{}
		// declaredHeads maps each binding name to its declared type's head, "" when any binding
		// of the name is unannotated or has another head.
		declaredHeads := map[string]string{}
		declare := func(name string, typ ast.TypeExpr) {
			head := fillMayAdoptTypeHead(typ)
			if prior, seen := declaredHeads[name]; seen && prior != head {
				head = ""
			}
			declaredHeads[name] = head
		}
		for _, param := range decl.Params {
			valueParams[param.Name] = true
			declare(param.Name, param.Type)
		}
		// The passes below read the same body nodes. Collect them once and reuse the
		// buffer for the next function.
		bodyNodes = bodyNodes[:0]
		fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(node any) {
			switch node.(type) {
			case *ast.LambdaExpr, *ast.VarDeclStmt, *ast.ForStmt, *ast.IterForStmt,
				*ast.FuncDecl, *ast.MatchStmt, *ast.MatchExpr, *ast.CatchExpr,
				*ast.Ident, *ast.FieldExpr, *ast.CallExpr:
				bodyNodes = append(bodyNodes, node)
			}
		})
		forEachBodyNode := func(visit func(any)) {
			for _, node := range bodyNodes {
				visit(node)
			}
		}
		hasLocalShadowCandidate := false
		forEachBodyNode(func(v any) {
			switch n := v.(type) {
			case *ast.LambdaExpr:
				for _, param := range n.Params {
					valueParams[param.Name] = true
					declare(param.Name, param.Type)
				}
			case *ast.VarDeclStmt:
				declare(n.Name, n.Type)
				if len(byName[n.Name]) != 0 || a.fillMayAdoptNamedCallee(n.Name) {
					hasLocalShadowCandidate = true
				}
			case *ast.ForStmt:
				declare(n.Name, nil)
			case *ast.IterForStmt:
				for name := range fillMayAdoptIterBinders(n) {
					declare(name, nil)
				}
			}
		})
		// A parameter shadows a function of its name throughout the body (lambdas capture it;
		// a body holds no nested `def`, and one would end the shadowing, so it disables all of it).
		shadowed := map[string]bool{}
		for _, param := range decl.Params {
			shadowed[param.Name] = true
		}
		// A match arm's payload binders shadow it within the arm's guard and body, a loop binder
		// within the loop's filters and body, and a local within the statements after its
		// declaration in the same block.
		var armShadowed map[*ast.Ident]bool
		markScoped := func(binders map[string]bool, scopes ...any) {
			if len(binders) == 0 {
				return
			}
			// Shadowing can affect this graph only for a name that otherwise
			// resolves to a known callee. Other call names are already unknown,
			// and other identifiers cannot add a caller edge.
			interesting := false
			for name := range binders {
				if len(byName[name]) != 0 || a.fillMayAdoptNamedCallee(name) {
					interesting = true
					break
				}
			}
			if !interesting {
				return
			}
			// A binder set applies to every scope in this call. Sharing the
			// visited set avoids revisiting syntax shared between those scopes.
			state := fillMayAdoptSeenPool.Get().(*fillWalkSeen)
			visit := func(inner any) {
				if ident, ok := inner.(*ast.Ident); ok && ident != nil && binders[ident.Name] {
					if armShadowed == nil {
						armShadowed = make(map[*ast.Ident]bool, min(len(bodyNodes)/8, 32))
					}
					armShadowed[ident] = true
				}
			}
			for _, scope := range scopes {
				fillMayAdoptWalkSeen(reflect.ValueOf(scope), visit, state.values)
			}
			if state.reset() {
				fillMayAdoptSeenPool.Put(state)
			}
		}
		// One pass per list: a statement's idents are shadowed by every local declared before it
		// in the list. (Walking the remaining statements once per local was quadratic in block
		// length: ~3% of compiling the stage1 driver.)
		if hasLocalShadowCandidate {
			fillMayAdoptStmtLists(reflect.ValueOf(decl.Body), func(list []ast.Stmt) {
				var declaredBefore map[string]bool
				for _, stmt := range list {
					if len(declaredBefore) != 0 {
						markScoped(declaredBefore, stmt)
					}
					if local, ok := stmt.(*ast.VarDeclStmt); ok && local != nil {
						// Keep only names that can change callee classification. A
						// declaration of any other name cannot create an edge or
						// turn an already unknown call into a known one.
						if len(byName[local.Name]) == 0 && !a.fillMayAdoptNamedCallee(local.Name) {
							continue
						}
						if declaredBefore == nil {
							declaredBefore = map[string]bool{}
						}
						declaredBefore[local.Name] = true
					}
				}
			})
		}
		markMatchArm := func(arm *ast.MatchArm) {
			binders := map[string]bool{}
			switch pattern := arm.Pattern.(type) {
			case *ast.MatchVariantPattern, *ast.MatchStructPattern:
				collectMatchPatternBindNames(pattern, binders)
			}
			markScoped(binders, arm.Guard, arm.Body)
		}
		nestedDef := false
		forEachBodyNode(func(v any) {
			switch n := v.(type) {
			case *ast.FuncDecl:
				nestedDef = true
			case *ast.ForStmt:
				markScoped(map[string]bool{n.Name: true}, n.Body)
			case *ast.IterForStmt:
				markScoped(fillMayAdoptIterBinders(n), n.WhereFilter, n.Filter, n.Body)
			// Arms are held by value, so they are reached through their match, never visited alone.
			case *ast.MatchStmt:
				for i := range n.Arms {
					markMatchArm(&n.Arms[i])
				}
			case *ast.MatchExpr:
				for i := range n.Arms {
					markMatchArm(&n.Arms[i])
				}
			case *ast.CatchExpr:
				for _, arm := range n.Arms {
					binders := map[string]bool{}
					for _, name := range arm.Payload {
						binders[name] = true
					}
					markScoped(binders, arm.Body)
				}
			}
		})
		if nestedDef {
			shadowed = map[string]bool{}
			armShadowed = map[*ast.Ident]bool{}
		}
		addCaller := func(callee *ast.FuncDecl) {
			edges := callers[callee]
			// All mentions in one caller are collected consecutively.
			if len(edges) == 0 || edges[len(edges)-1] != decl {
				callers[callee] = append(edges, decl)
			}
		}
		unknown := false
		forEachBodyNode(func(v any) {
			switch n := v.(type) {
			case *ast.Ident:
				// Any mention, not only a callee: a function referenced as a value may be called later.
				if shadowed[n.Name] || armShadowed[n] {
					break
				}
				for _, callee := range byName[n.Name] {
					addCaller(callee)
				}
			case *ast.FieldExpr:
				// `recv.m`: a builtin-container receiver never selects a method whose receiver
				// parameter is a declared struct (the darray `push` is not InlineVec's).
				candidates := byName[n.Field]
				if len(candidates) == 0 {
					break
				}
				receiverHead := ""
				if root, ok := fillMayAdoptStripParens(n.Object).(*ast.Ident); ok && root != nil {
					receiverHead = declaredHeads[root.Name]
				}
				builtinReceiver := fillMayAdoptBuiltinHeads[receiverHead]
				for _, callee := range candidates {
					if builtinReceiver && len(callee.Params) > 0 {
						head, known := firstParamHeads[callee]
						if !known {
							head = fillMayAdoptTypeHead(callee.Params[0].Type)
							firstParamHeads[callee] = head
						}
						if head != receiverHead && structNames[head] {
							continue
						}
					}
					addCaller(callee)
				}
			case *ast.CallExpr:
				call := n
				if call == nil || unknown {
					return
				}
				target := call.Func
				if special, ok := target.(*ast.SpecializeExpr); ok && special != nil {
					target = special.Operand
				}
				switch callee := target.(type) {
				case *ast.Ident:
					if fillMayAdoptIntrinsics[callee.Name] {
						break
					}
					// A parser-lowered f-string: the `__fstr` builtin reads its parts and returns a
					// fresh dstr. A user function of that name is not lowered and resolves normally.
					if call.FStringLowering && callee.Name == "__fstr" {
						break
					}
					// A parameter, local or pattern binder holds a function value, whatever global
					// shares its name.
					_, local := declaredHeads[callee.Name]
					if valueParams[callee.Name] || local || armShadowed[callee] {
						unknown = true
					} else if len(byName[callee.Name]) == 0 && !a.fillMayAdoptNamedCallee(callee.Name) {
						unknown = true
					}
				case *ast.FieldExpr:
					if funcFields[callee.Field] {
						unknown = true
					}
				default:
					unknown = true
				}
			}
		})
		if unknown {
			seed(decl)
		}
	}
	for len(work) > 0 {
		decl := work[len(work)-1]
		work = work[:len(work)-1]
		for _, caller := range callers[decl] {
			seed(caller)
		}
	}
	return result
}
