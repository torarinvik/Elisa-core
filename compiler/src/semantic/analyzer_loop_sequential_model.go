package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Sequential loop-body model: a fallback for the invariant and termination provers when the
// simultaneous-substitution capture (captureLoopBodyEffectMode) and its guard-`if` path enumeration
// decline. It symbolically executes the body statement by statement:
//
//   - an immutable body-local `name: T = v` binds name to v composed with the state so far (a let);
//   - `x <- v` on a scalar local/param rebinds x to v composed with the state so far, so later reads
//     (and later writes to x) see the sequential value, expressed over the iteration's PRE-state;
//   - an `if`/`elif`/`else` forks the path; each guard is composed with the state at the `if`, so a
//     guard reading a let or an earlier write means its value at that point, never the pre-state;
//   - `break`/`return` end the path as exiting.
//
// Every expression it admits is effect-free except for calls to resolved functions with no mutable
// or linear reference parameter: such a callee cannot write any body local or parameter, so the
// model's state is exact. Anything else (stores, method calls, address-of, `continue`, nested loops,
// mutable lets, writes to reference-typed or non-local roots) declines the whole model.

const (
	seqLoopMaxPaths = 16
	seqLoopMaxDepth = 6
)

type seqLoopFrag struct {
	conds  []ast.Expr
	state  map[string]ast.Expr // assigned scalars, over the pre-state
	lets   map[string]ast.Expr // body-local immutable bindings, over the pre-state
	exited bool
}

func (f seqLoopFrag) clone() seqLoopFrag {
	out := seqLoopFrag{conds: append([]ast.Expr{}, f.conds...), state: map[string]ast.Expr{}, lets: map[string]ast.Expr{}, exited: f.exited}
	for k, v := range f.state {
		out.state[k] = v
	}
	for k, v := range f.lets {
		out.lets[k] = v
	}
	return out
}

func (f seqLoopFrag) env() map[string]ast.Expr {
	env := make(map[string]ast.Expr, len(f.state)+len(f.lets))
	for k, v := range f.state {
		env[k] = v
	}
	for k, v := range f.lets {
		env[k] = v
	}
	return env
}

// sequentialLoopBodyPaths returns the body's paths with composed guards and net substitutions.
func (a *Analyzer) sequentialLoopBodyPaths(body []ast.Stmt) ([]loopBodyPath, bool) {
	if a == nil || a.currentScope == nil {
		return nil, false
	}
	stmts := stripLeadingContracts(body)
	if len(stmts) == 0 {
		return nil, false
	}
	frags, ok := a.seqLoopWalk(stmts, []seqLoopFrag{{state: map[string]ast.Expr{}, lets: map[string]ast.Expr{}}}, 0)
	if !ok || len(frags) == 0 || len(frags) > seqLoopMaxPaths {
		return nil, false
	}
	out := make([]loopBodyPath, 0, len(frags))
	for _, f := range frags {
		path := loopBodyPath{conds: f.conds, exits: f.exited}
		if !f.exited {
			path.subst = f.state
		}
		out = append(out, path)
	}
	return out, true
}

func (a *Analyzer) seqLoopWalk(stmts []ast.Stmt, frontier []seqLoopFrag, depth int) ([]seqLoopFrag, bool) {
	if depth > seqLoopMaxDepth {
		return nil, false
	}
	for _, s := range stmts {
		if len(frontier) > seqLoopMaxPaths {
			return nil, false
		}
		switch n := s.(type) {
		case *ast.VarDeclStmt:
			if n == nil || n.Mutable || n.Ghost || n.Value == nil || n.Name == "" {
				return nil, false
			}
			if _, bound := a.currentScope.Lookup(n.Name); bound {
				return nil, false // a let shadowing a visible name would change what later reads mean
			}
			if !a.seqLoopValueAdmissible(n.Value) {
				return nil, false
			}
			for i := range frontier {
				if frontier[i].exited {
					continue
				}
				if _, again := frontier[i].lets[n.Name]; again {
					return nil, false
				}
				v, ok := substituteLemmaEnsure(n.Value, frontier[i].env())
				if !ok {
					return nil, false
				}
				frontier[i].lets[n.Name] = v
			}
		case *ast.AssignStmt:
			target, ok := n.Target.(*ast.Ident)
			if !ok || target == nil || n.Value == nil || !a.seqLoopWritableRoot(target.Name) {
				return nil, false
			}
			if n.Optional {
				return nil, false
			}
			if !a.seqLoopValueAdmissible(n.Value) {
				return nil, false
			}
			for i := range frontier {
				if frontier[i].exited {
					continue
				}
				if _, isLet := frontier[i].lets[target.Name]; isLet {
					return nil, false
				}
				v, ok := substituteLemmaEnsure(n.Value, frontier[i].env())
				if !ok {
					return nil, false
				}
				frontier[i].state[target.Name] = v
			}
		case *ast.IfStmt:
			if n == nil || n.Cond == nil {
				return nil, false
			}
			type arm struct {
				cond ast.Expr
				body []ast.Stmt
			}
			arms := []arm{{n.Cond, n.Then}}
			for _, e := range n.Elifs {
				if e.Cond == nil {
					return nil, false
				}
				arms = append(arms, arm{e.Cond, e.Body})
			}
			for _, arm := range arms {
				if !a.seqLoopValueAdmissible(arm.cond) {
					return nil, false
				}
			}
			var next []seqLoopFrag
			for _, f := range frontier {
				if f.exited {
					next = append(next, f)
					continue
				}
				env := f.env()
				var negated []ast.Expr
				for _, arm := range arms {
					cond, ok := substituteLemmaEnsure(arm.cond, env)
					if !ok {
						return nil, false
					}
					taken := f.clone()
					taken.conds = append(taken.conds, negated...)
					taken.conds = append(taken.conds, cond)
					sub, ok := a.seqLoopWalk(arm.body, []seqLoopFrag{taken}, depth+1)
					if !ok {
						return nil, false
					}
					next = append(next, sub...)
					negated = append(negated, &ast.UnaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_NOT, Operand: &ast.ParenExpr{Position: cond.Pos(), Inner: cond}})
				}
				rest := f.clone()
				rest.conds = append(rest.conds, negated...)
				sub, ok := a.seqLoopWalk(n.Else, []seqLoopFrag{rest}, depth+1)
				if !ok {
					return nil, false
				}
				next = append(next, sub...)
				if len(next) > seqLoopMaxPaths {
					return nil, false
				}
			}
			frontier = next
		case *ast.BreakStmt, *ast.ReturnStmt:
			for i := range frontier {
				frontier[i].exited = true
			}
		case *ast.ExprStmt:
			if !a.loopBodyAssertIsPure(n) {
				return nil, false
			}
			// Body-local immutable names are tracked per path. A local named `assert` or `ASSERT`
			// shadows the builtin on that path even though it is not in the enclosing scope used by
			// assertedCondition; decline rather than misclassifying a user call as a proof-only check.
			for _, frag := range frontier {
				if _, shadowed := frag.lets["assert"]; shadowed {
					return nil, false
				}
				if _, shadowed := frag.lets["ASSERT"]; shadowed {
					return nil, false
				}
			}
		case *ast.PassStmt:
		default:
			return nil, false
		}
	}
	return frontier, true
}

// seqLoopWritableRoot admits a write target the model tracks exactly: a mutable, non-reference
// local or parameter, which no admitted callee can reach.
func (a *Analyzer) seqLoopWritableRoot(name string) bool {
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || sym.Type == nil {
		return false
	}
	if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
		return false
	}
	if _, isRef := StripAggregateStateType(sym.Type).(*RefType); isRef {
		return false
	}
	return (IsNumericType(sym.Type) && !IsFloatType(sym.Type)) || IsBoolType(sym.Type)
}

// seqLoopValueAdmissible admits reads, arithmetic, slices, casts and calls to resolved free
// functions none of whose parameters is a mutable or linear reference.
func (a *Analyzer) seqLoopValueAdmissible(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit:
		return true
	case *ast.Ident:
		return n != nil && !a.seqLoopNameIsMutableGlobal(n.Name)
	case *ast.ParenExpr:
		return n != nil && a.seqLoopValueAdmissible(n.Inner)
	case *ast.FieldExpr:
		return n != nil && !n.Safe && a.seqLoopValueAdmissible(n.Object)
	case *ast.IndexExpr:
		return n != nil && n.Index2 == nil && n.Fallback == nil && !n.LegacyElseFallback &&
			n.AsSpecialize == nil && n.AsOverlayCall == nil &&
			a.seqLoopValueAdmissible(n.Object) && a.seqLoopValueAdmissible(n.Index)
	case *ast.SliceExpr:
		return n != nil && a.seqLoopValueAdmissible(n.Object) &&
			(n.Start == nil || a.seqLoopValueAdmissible(n.Start)) &&
			(n.End == nil || a.seqLoopValueAdmissible(n.End))
	case *ast.UnaryExpr:
		return n != nil && n.Op != lexer.TOKEN_AMPERSAND && a.seqLoopValueAdmissible(n.Operand)
	case *ast.BinaryExpr:
		return n != nil && a.seqLoopValueAdmissible(n.Left) && a.seqLoopValueAdmissible(n.Right)
	case *ast.CastExpr:
		return n != nil && isPureNumericValueCast(n) && a.seqLoopValueAdmissible(n.Operand)
	case *ast.TernaryExpr:
		return n != nil && a.seqLoopValueAdmissible(n.Cond) && a.seqLoopValueAdmissible(n.Value) && a.seqLoopValueAdmissible(n.Alt)
	case *ast.CallExpr:
		if n == nil {
			return false
		}
		if _, ok := n.Func.(*ast.Ident); !ok {
			return false
		}
		decl, ok := a.resolveDirectCallFuncDecl(n)
		if !ok || decl == nil {
			return false
		}
		sym, ok := a.symbolForFuncDecl(decl)
		if !ok || sym == nil {
			return false
		}
		fnType, ok := sym.Type.(*FuncType)
		if !ok || fnType == nil || len(fnType.Params) != len(n.Args) {
			return false
		}
		for _, p := range fnType.Params {
			if ref, isRef := StripAggregateStateType(p).(*RefType); isRef && (ref.Mutable || ref.Linear) {
				return false
			}
		}
		for _, arg := range n.Args {
			if !a.seqLoopValueAdmissible(arg) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// typeSequentialPathsQuietly types the composed (pre-state) guard and update expressions on the
// pre-loop scope so the SMT translator sees their widths; diagnostics and proof records of this
// typing pass are discarded.
func (a *Analyzer) typeSequentialPathsQuietly(paths []loopBodyPath) {
	savedSuppress := a.suppressDiagnostics
	savedProofs := len(a.proofReport)
	a.suppressDiagnostics = true
	a.ghostReadAllowed++
	defer func() {
		a.suppressDiagnostics = savedSuppress
		a.ghostReadAllowed--
		a.proofReport = a.proofReport[:savedProofs]
	}()
	for _, p := range paths {
		for _, c := range p.conds {
			if a.exprTypes[c] == nil {
				a.analyzeCondExpr(c)
			}
		}
		for _, v := range p.subst {
			if a.exprTypes[v] == nil {
				a.analyzeExpr(v)
			}
		}
	}
}

// seqLoopNameIsMutableGlobal reports a name bound to global storage, which an admitted callee may
// write; the model would read such a name as unchanged across the call.
func (a *Analyzer) seqLoopNameIsMutableGlobal(name string) bool {
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil {
		return false
	}
	return sym.Kind == SymbolGlobal || sym.Kind == SymbolExternVar
}

// seqLoopSpecsReadNoGlobals checks the loop's guard and clauses for global-storage reads.
func (a *Analyzer) seqLoopSpecsReadNoGlobals(exprs ...ast.Expr) bool {
	names := map[string]bool{}
	for _, e := range exprs {
		if e != nil {
			collectExprIdentNames(e, names)
		}
	}
	for name := range names {
		if a.seqLoopNameIsMutableGlobal(name) {
			return false
		}
	}
	return true
}
