package semantic

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/regionframesyntax"
)

// Frame-block backing for small default reserve_commit stacks.
//
// A default (docs/73) reserve_commit stack reserves its contiguous range lazily on first
// allocation, which costs a locked reservation-cache round trip per call even for a handful of
// elements. When the stack's single growable has a PROVEN maximum element count, codegen can
// instead evaluate that bound at region entry and, when the footprint fits, back the stack with
// a block in the function's own frame (arena_init_stack_block). The stack keeps reserve_commit
// semantics either way: one contiguous block, base never moves, overflow panics. A bound that
// does not fit (or cannot be evaluated without overflow) leaves today's lazy path untouched, so
// the bound is advisory: it only ever selects a cheaper block of sufficient size.
//
// Soundness needs the bound to cover EVERY byte the stack's arena receives, so eligibility is
// deliberately narrow:
//   - the element type is numeric or bool (no owned payload allocations land in the arena);
//   - the name is initialized by `[]`, a list literal, or a single-binder list comprehension
//     over `0..<K` (optionally filtered), and grows only through counting-loop pushes — every
//     such site multiplies by its enclosing counting loops, as hardBoundForName does;
//   - `name.resize(n)` is also accepted when flow facts at the call proved a constant upper
//     bound on n (recordResizeArgUpperBound). resize SETS the count, so the site contributes
//     that bound once, wherever it sits (loops included), rather than multiplying by loops;
//   - every other mention of the name is an element read/write `name[i]`, a `.count`/`.len`
//     read, or a `for x in name` source. Passing it anywhere, copying, rebinding, or taking an
//     address disqualifies (the darray could then grow or be refilled out of sight).

// stackBlockAdvisoryBound returns the proven element-count bound for `name`, or ok=false.
func (a *Analyzer) stackBlockAdvisoryBound(body []ast.Stmt, name string, elem Type, paramNames map[string]bool) (ast.Expr, bool) {
	if !stackBlockElemEligible(elem) {
		return nil, false
	}
	s := &stackBlockScan{hardBoundScan: hardBoundScan{name: name}, paramNames: paramNames, upperBounds: a.stackBlockUpperBound}
	s.walk(reflect.ValueOf(body))
	if s.disqualified || s.decls != 1 || s.bound == nil || s.mentions != s.allowed {
		return nil, false
	}
	if !stackBlockBoundEvaluable(s.bound, paramNames) {
		return nil, false
	}
	return s.bound, true
}

func stackBlockElemEligible(t Type) bool {
	if IsNumericType(t) {
		return true
	}
	b, ok := t.(*BuiltinType)
	return ok && b.Name == "bool"
}

type stackBlockScan struct {
	hardBoundScan
	decls    int
	mentions int // Ident occurrences of name
	allowed  int // occurrences in an allowed position
	// upperBounds: proven constant upper bound of each resize argument / comprehension end.
	upperBounds map[ast.Expr]int64
	paramNames  map[string]bool
}

func (s *stackBlockScan) addSite(site ast.Expr, pos lexer.Pos) {
	for i := len(s.loopStack) - 1; i >= 0; i-- {
		b, ok := countingLoopBound(s.loopStack[i])
		if !ok {
			s.disqualified = true
			return
		}
		site = semanticMultiplyReserveExpr(b, site, pos)
	}
	s.bound = semanticAddReserveExpr(s.bound, site, pos)
}

func (s *stackBlockScan) declInit(vd *ast.VarDeclStmt) {
	s.decls++
	switch v := stripParenExpr(vd.Value).(type) {
	case *ast.ListLitExpr:
		if v.Brace {
			s.disqualified = true
			return
		}
		s.addSite(semanticIntReserveExpr(len(v.Elems), vd.Position), vd.Position)
	case *ast.ListComprehensionExpr:
		if v.Key != nil || v.Set || v.SecondName != "" || v.Parallel || v.Owner != nil ||
			v.RangeEnd == nil || v.RangeStep != nil || v.RangeOp != lexer.TOKEN_RANGE_LT {
			s.disqualified = true
			return
		}
		if start, ok := stripParenExpr(v.Source).(*ast.IntLit); !ok || start.Value != "0" {
			s.disqualified = true
			return
		}
		// A range end that codegen cannot evaluate at region entry (a local computed later)
		// still sizes the block when flow facts proved a constant upper bound for it.
		if hi, ok := s.upperBounds[v]; ok && !stackBlockBoundEvaluable(v.RangeEnd, s.paramNames) {
			if hi < 0 || hi > stackBlockMaxConstElems {
				s.disqualified = true
				return
			}
			s.addSite(semanticIntReserveExpr(int(hi), vd.Position), vd.Position)
			return
		}
		s.addSite(v.RangeEnd, vd.Position)
	default:
		s.disqualified = true
	}
}

func (s *stackBlockScan) isName(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == s.name
}

func (s *stackBlockScan) walk(v reflect.Value) {
	if s.disqualified || !v.IsValid() || !v.CanInterface() {
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			s.walk(v.Elem())
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		switch n := v.Interface().(type) {
		case *ast.VarDeclStmt:
			if n.Name == s.name {
				s.declInit(n)
				if s.disqualified {
					return
				}
			}
		case *ast.Ident:
			if n.Name == s.name {
				s.mentions++
			}
			return
		case *ast.IndexExpr:
			if s.isName(n.Object) && n.Index2 == nil {
				s.allowed++
			}
		case *ast.FieldExpr:
			if s.isName(n.Object) {
				switch n.Field {
				case "count", "len":
					s.allowed++
				case "push", "resize":
					s.allowed++ // bounded by checkPush below (unbounded sites disqualify)
				}
			}
		case *ast.CallExpr:
			s.checkPush(n)
		case *ast.LambdaExpr:
			// A closure can run any number of times from anywhere: a mention inside one is
			// never bounded by the enclosing loops.
			inner := &stackBlockScan{hardBoundScan: hardBoundScan{name: s.name}}
			inner.walk(v.Elem())
			if inner.mentions > 0 || inner.decls > 0 {
				s.disqualified = true
			}
			return
		case *ast.IterForStmt:
			if s.isName(n.Source) {
				s.allowed++
			}
			s.loopStack = append(s.loopStack, n)
			s.walk(v.Elem())
			s.loopStack = s.loopStack[:len(s.loopStack)-1]
			return
		case *ast.ForStmt:
			s.loopStack = append(s.loopStack, n)
			s.walk(v.Elem())
			s.loopStack = s.loopStack[:len(s.loopStack)-1]
			return
		default:
			if node, ok := n.(ast.Node); ok && isLoopStmtNode(node) {
				s.loopStack = append(s.loopStack, node)
				s.walk(v.Elem())
				s.loopStack = s.loopStack[:len(s.loopStack)-1]
				return
			}
		}
		s.walk(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			s.walk(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			s.walk(v.Index(i))
		}
	}
}

// checkPush sizes a push site. Any other method on the name is not an allowed mention (its
// FieldExpr was not counted), so it disqualifies through the mention/allowed balance.
func (s *stackBlockScan) checkPush(call *ast.CallExpr) {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || !s.isName(field.Object) {
		return
	}
	switch field.Field {
	case "push":
		s.addSite(semanticIntReserveExpr(hardBoundPushElementCount(call), call.Pos()), call.Pos())
	case "resize":
		hi, ok := s.upperBounds[call]
		if !ok || hi < 0 || hi > stackBlockMaxConstElems {
			s.disqualified = true
			return
		}
		// resize sets the count to at most hi; it never accumulates across iterations.
		s.bound = semanticAddReserveExpr(s.bound, semanticIntReserveExpr(int(hi), call.Pos()), call.Pos())
	}
}

// stackBlockMaxConstElems caps a recorded resize bound well below any overflow in the
// footprint arithmetic; codegen applies the real byte limit.
const stackBlockMaxConstElems = 1 << 20

// recordResizeArgUpperBound remembers the constant upper bound flow facts prove for a
// `darray.resize(n)` argument at the call (e.g. after `if n > 1600: return -1`). Only a plain
// identifier or integer literal is considered; anything else records nothing (sound: the
// stack simply keeps its lazy reservation).
func (a *Analyzer) recordResizeArgUpperBound(call *ast.CallExpr) {
	if a == nil || call == nil || len(call.Args) != 1 {
		return
	}
	a.recordStackBlockUpperBound(call, call.Args[0])
}

// recordComprehensionEndUpperBound does the same for a `[v for _ in 0..<n]` range end, so a
// darray sized by a local (not a parameter) can still take an exactly-sized frame block.
func (a *Analyzer) recordComprehensionEndUpperBound(expr *ast.ListComprehensionExpr) {
	if a == nil || expr == nil || expr.RangeEnd == nil {
		return
	}
	a.recordStackBlockUpperBound(expr, expr.RangeEnd)
}

func (a *Analyzer) recordStackBlockUpperBound(site ast.Expr, bound ast.Expr) {
	var hi int64
	switch arg := stripParenExpr(bound).(type) {
	case *ast.IntLit:
		v, ok := parseStackBlockIntLit(arg.Value)
		if !ok {
			return
		}
		hi = v
	case *ast.Ident:
		r, ok := a.rangeForBranchExpr(arg)
		if !ok || !r.hiKnown {
			return
		}
		hi = r.hi
	default:
		return
	}
	if hi < 0 {
		return
	}
	if a.stackBlockUpperBound == nil {
		a.stackBlockUpperBound = map[ast.Expr]int64{}
	}
	a.stackBlockUpperBound[site] = hi
}

func parseStackBlockIntLit(text string) (int64, bool) {
	return regionframesyntax.ParseStackBlockIntLit(text)
}
func stackBlockBoundEvaluable(expr ast.Expr, params map[string]bool) bool {
	return regionframesyntax.StackBlockBoundEvaluable(expr, params)
}
func StackBlockIntegerTypeName(t ast.TypeExpr) string {
	return regionframesyntax.StackBlockIntegerTypeName(t)
}
