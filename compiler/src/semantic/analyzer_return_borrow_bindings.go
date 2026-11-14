package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"os"
	"reflect"
)

// returnBorrowRefDeclTarget is the local a reference declaration `r: mutable T& = &x.f` borrows
// from, or "" when the declaration is not a reference bound to the address of a local place.
func (a *Analyzer) returnBorrowRefDeclTarget(n *ast.VarDeclStmt) (*ast.AddrOfExpr, string) {
	addr, ok := returnBorrowStripParens(n.Value).(*ast.AddrOfExpr)
	if !ok || addr == nil {
		return nil, ""
	}
	return addr, returnBorrowPlaceRootName(addr.Operand)
}

// returnBorrowRefDeclLinks maps each reference local declared from `&place` to the locals its
// place is rooted at. Name-based, like the untrusted census: an extra link only forwards more.
func (a *Analyzer) returnBorrowRefDeclLinks(fn *ast.FuncDecl) map[string][]string {
	links := map[string][]string{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if decl, ok := v.Interface().(*ast.VarDeclStmt); ok && decl != nil && decl.Name != "" && v.Kind() == reflect.Pointer {
				if _, root := a.returnBorrowRefDeclTarget(decl); root != "" && root != decl.Name {
					links[decl.Name] = append(links[decl.Name], root)
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).CanInterface() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, stmt := range fn.Body {
		walk(reflect.ValueOf(stmt))
	}
	return links
}

// defineReturnBorrowBinding defines a walker local and seeds its alias and store union.
func (a *Analyzer) defineReturnBorrowBinding(aliases map[string]returnBorrowFlow, name string, node ast.Node, typ Type, flow returnBorrowFlow) {
	if name == "" || name == "_" {
		return
	}
	if a.currentScope != nil {
		a.currentScope.Define(&Symbol{Name: name, Kind: SymbolLocal, Type: typ, Node: node})
	}
	a.setReturnBorrowAlias(aliases, name, flow, false)
}

// returnBorrowConservativeFlow is the flow of a value the walk cannot trace: a frame borrow
// whenever its type can hold one.
func (a *Analyzer) returnBorrowConservativeFlow(expr ast.Expr, fallback Type) returnBorrowFlow {
	t := a.exprType(expr)
	if t == nil {
		t = fallback
	}
	if t == nil || a.typeMayHoldFrameBorrow(t, map[Type]bool{}) {
		if storeScanTrace && expr != nil {
			fn := ""
			if a.currentFuncDecl != nil {
				fn = a.currentFuncDecl.Name
			}
			fmt.Fprintf(os.Stderr, "RBCONSERV %s %T at %s type=%s\n", fn, expr, expr.Pos(), typeString(t))
		}
		return returnBorrowFlow{Local: true}
	}
	return returnBorrowFlow{}
}

// returnBorrowRecordingIdentFlow reads a name while recording: its walker alias joined with the
// store union of the binding it resolves to.
func (a *Analyzer) returnBorrowRecordingIdentFlow(ident *ast.Ident, aliases map[string]returnBorrowFlow) returnBorrowFlow {
	if a.returnBorrowDeclConservative[ident.Name] {
		return a.returnBorrowConservativeFlow(ident, nil)
	}
	flow, hasAlias := aliases[ident.Name]
	var sym *Symbol
	if a.currentScope != nil {
		sym, _ = a.currentScope.Lookup(ident.Name)
	}
	if sym == nil {
		if hasAlias {
			return mergeReturnBorrowFlow(flow, a.returnBorrowConservativeFlow(ident, nil))
		}
		if a.returnBorrowNamesConst(ident.Name) {
			// A module constant is static storage: it holds no frame borrow.
			return returnBorrowFlow{}
		}
		return a.returnBorrowConservativeFlow(ident, nil)
	}
	if sym.Kind != SymbolLocal {
		return flow
	}
	if sym.Node == nil {
		return mergeReturnBorrowFlow(flow, a.returnBorrowConservativeFlow(ident, sym.Type))
	}
	return mergeReturnBorrowFlow(flow, a.returnBorrowDeclRecording[returnBorrowBinderKey{node: sym.Node, name: ident.Name}])
}

// returnBorrowNamesConst reports whether a name the walk did not bind is a visible module
// constant (a declared const is stored module-qualified, so the walk's scope misses it).
func (a *Analyzer) returnBorrowNamesConst(name string) bool {
	if a.globalScope == nil {
		return false
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		if sym, ok := a.globalScope.Lookup(candidate); ok && sym != nil {
			return sym.Kind == SymbolConst
		}
	}
	return false
}

// returnBorrowEnvSymbolFlow is the recorded store union of a trusted local binding. Reference
// declarations are excluded: their value is an address, not the union of what was stored.
func (a *Analyzer) returnBorrowEnvSymbolFlow(sym *Symbol) (returnBorrowFlow, bool) {
	if !a.returnBorrowEnvActive() || sym == nil || sym.Kind != SymbolLocal || sym.Node == nil {
		return returnBorrowFlow{}, false
	}
	if _, isRef := sym.Type.(*RefType); isRef {
		// A pattern binder is never rebound, so its union is the bound value itself.
		if _, isDecl := sym.Node.(*ast.VarDeclStmt); isDecl {
			return returnBorrowFlow{}, false
		}
	}
	flow, ok := a.returnBorrowDeclEnv[returnBorrowBinderKey{node: sym.Node, name: sym.Name}]
	return flow, ok
}

// returnBorrowEnvTrustedRoot resolves the local a place is rooted at when it is env-trusted.
func (a *Analyzer) returnBorrowEnvTrustedRoot(expr ast.Expr) (*ast.Ident, returnBorrowFlow, bool) {
	if !a.returnBorrowEnvActive() || a.currentScope == nil {
		return nil, returnBorrowFlow{}, false
	}
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
			continue
		case *ast.MoveExpr:
			expr = n.Operand
			continue
		case *ast.FieldExpr:
			expr = n.Object
			continue
		case *ast.IndexExpr:
			expr = n.Object
			continue
		case *ast.SliceExpr:
			expr = n.Object
			continue
		}
		break
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return nil, returnBorrowFlow{}, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return nil, returnBorrowFlow{}, false
	}
	flow, ok := a.returnBorrowEnvSymbolFlow(sym)
	return ident, flow, ok
}

// returnBorrowModeledBinderNodes lists the binding nodes the walker defines besides declarations:
// iterator-loop patterns and declaring tuple bindings on statement paths it walks.
// returnBorrowMarkConditionBinders marks the condition unwraps defineReturnBorrowConditionBindings
// binds; the two must agree on which binders the walk models.
func returnBorrowMarkConditionBinders(cond ast.Expr, modeled map[ast.Node]bool) bool {
	switch n := cond.(type) {
	case *ast.ParenExpr:
		return returnBorrowMarkConditionBinders(n.Inner, modeled)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			left := returnBorrowMarkConditionBinders(n.Left, modeled)
			if left && modeled == nil {
				return true
			}
			right := returnBorrowMarkConditionBinders(n.Right, modeled)
			return left || right
		case lexer.TOKEN_OR:
			// The analysis defines the alternatives' shared binders under the or-expression.
			if modeled != nil {
				modeled[n] = true
			}
			return true
		case lexer.TOKEN_IS:
			if _, _, pattern, ok := unwrapDirectConditionPattern(n); ok && pattern != nil {
				binders := returnBorrowMatchPatternBinders(pattern, nil)
				for _, binder := range binders {
					if modeled == nil {
						return true
					}
					modeled[binder.node] = true
				}
				return len(binders) != 0
			}
		}
	case *ast.OptionalBindExpr:
		if n.Name != "" && n.Name != "_" {
			if modeled != nil {
				modeled[n] = true
			}
			return true
		}
	}
	return false
}

// returnBorrowMatchBinder is a match-arm binder the return-borrow walk models: its value is a copy
// of the scrutinee's payload, except a `...rest` binder, which is a view into the scrutinee.
type returnBorrowMatchBinder struct {
	name string
	node ast.Node
	view bool
}

// returnBorrowMatchPatternBinders lists the binders of a match pattern the walk models. An
// or-pattern's binders are left out (they stay conservative).
func returnBorrowMatchPatternBinders(pattern ast.MatchPattern, out []returnBorrowMatchBinder) []returnBorrowMatchBinder {
	keep := func(name string) bool { return name != "" && name != "_" }
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		if p.Binder != "" {
			if keep(p.Binder) {
				out = append(out, returnBorrowMatchBinder{name: p.Binder, node: p})
			}
		} else if keep(p.Name) {
			out = append(out, returnBorrowMatchBinder{name: p.Name, node: p})
		}
	case *ast.MatchVariantPattern:
		if keep(p.As) {
			out = append(out, returnBorrowMatchBinder{name: p.As, node: p})
		}
		for _, arg := range p.Args {
			out = returnBorrowMatchPatternBinders(arg.Pattern, out)
		}
	case *ast.MatchStructPattern:
		for _, arg := range p.Args {
			out = returnBorrowMatchPatternBinders(arg.Pattern, out)
		}
	case *ast.MatchTuplePattern:
		for _, elem := range p.Elems {
			out = returnBorrowMatchPatternBinders(elem, out)
		}
	case *ast.MatchListPattern:
		for _, elem := range p.Elems {
			out = returnBorrowMatchPatternBinders(elem, out)
		}
	case *ast.MatchRestPattern:
		if keep(p.Name) {
			out = append(out, returnBorrowMatchBinder{name: p.Name, node: p, view: true})
		}
	}
	return out
}

func returnBorrowModeledBinderNodes(fn *ast.FuncDecl) map[ast.Node]bool {
	modeled := map[ast.Node]bool{}
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch n := stmt.(type) {
			case *ast.IterForStmt:
				if n.Pattern != nil {
					modeled[n.Pattern] = true
				}
			case *ast.TupleBindStmt:
				if n.Declare {
					modeled[n] = true
				}
			case *ast.IfStmt:
				returnBorrowMarkConditionBinders(n.Cond, modeled)
				walk(n.Then)
				for _, clause := range n.Elifs {
					returnBorrowMarkConditionBinders(clause.Cond, modeled)
					walk(clause.Body)
				}
				walk(n.Else)
				continue
			case *ast.WhileStmt:
				returnBorrowMarkConditionBinders(n.Cond, modeled)
				walk(n.Body)
				continue
			case *ast.ForStmt:
				walk(n.Body)
				continue
			case *ast.MatchStmt:
				for _, arm := range n.Arms {
					for _, binder := range returnBorrowMatchPatternBinders(arm.Pattern, nil) {
						modeled[binder.node] = true
					}
					walk(arm.Body)
				}
				continue
			case *ast.ExprStmt:
				// A value-form loop's statements are walked like a nested body.
				for block, isBlock := n.Expr.(*ast.ExprBlock); isBlock && block != nil; block, isBlock = block.Value.(*ast.ExprBlock) {
					walk(block.Stmts)
				}
				continue
			case *ast.VarDeclStmt:
				// ... also as a declaration's value (`n: u32 = for c in xs |acc| -> acc:`).
				for block, isBlock := n.Value.(*ast.ExprBlock); isBlock && block != nil; block, isBlock = block.Value.(*ast.ExprBlock) {
					walk(block.Stmts)
				}
			}
			for _, body := range returnBorrowChildBlocks(stmt) {
				walk(body)
			}
		}
	}
	walk(fn.Body)
	return modeled
}

// returnBorrowWalkedStmt lists the statement kinds returnBorrowFlowForStatements models (or that
// cannot store anything: a MachineCoverageStmt only re-reads the input its machine's input var
// declaration already evaluated).
func returnBorrowWalkedStmt(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.VarDeclStmt, *ast.AssignStmt, *ast.ReturnStmt, *ast.ExprStmt, *ast.IfStmt, *ast.WhileStmt,
		*ast.ForStmt, *ast.IterForStmt, *ast.TupleBindStmt, *ast.AugAssignStmt, *ast.MatchStmt,
		*ast.CanStmt, *ast.ScopeStmt, *ast.RegionStmt, *ast.InStoreStmt, *ast.PoolStmt, *ast.LockStmt,
		*ast.CheckpointStmt, *ast.GroupedCheckpointStmt, *ast.StaticBlockStmt, *ast.ParallelForStmt,
		*ast.StaticIfStmt, *ast.BreakStmt, *ast.ContinueStmt, *ast.PassStmt, *ast.MachineCoverageStmt:
		return true
	}
	return false
}

// returnBorrowDiscardedCall returns the call of `_ = f(x)` (also `parser, _ <- parser.advance()`,
// whose lmut thread is claimed): its result is dropped, exactly like a call statement.
func (a *Analyzer) returnBorrowDiscardedCall(stmt ast.Stmt) *ast.CallExpr {
	discard, ok := stmt.(*ast.DiscardStmt)
	if !ok || discard == nil {
		return nil
	}
	return a.returnBorrowAsCall(discard.Value)
}

// returnBorrowAsCall returns the call an expression is: a CallExpr, or a `Name(args)` the parser
// read as a struct literal that analysis lowered to a call (a PascalCase function such as an
// extern `LLVMBuildCall2(...)`, a generic function, a cast or init hook). The lowered call shares
// the literal's argument nodes, so modeling it models the literal's stores.
func (a *Analyzer) returnBorrowAsCall(expr ast.Expr) *ast.CallExpr {
	switch n := returnBorrowStripParens(expr).(type) {
	case *ast.CallExpr:
		return n
	case *ast.StructLitExpr:
		if n != nil && a.loweredInitCalls != nil {
			return a.loweredInitCalls[n]
		}
	}
	return nil
}
