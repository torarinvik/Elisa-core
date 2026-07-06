package parser

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/120 §2 — declared lmut threading: `lmut` in return position is NOTATION.
//
//	def advance_char(lexer: lmut Lexer) -> (ch: char, lexer: lmut Lexer):
//	    ...
//	    return ch, lexer
//
// A return-tuple field spelled `name: lmut T` declares that the same-named `lmut`
// parameter threads back through this function — the explicit manifest for coarse
// boundaries. It is NOT a value slot: a real tuple return would copy the threaded
// struct (or demand affine move lowering on every return path) and change the ABI,
// buying ceremony with real cost. Instead this post-pass validates the manifest and
// ERASES it — from the return type and from every return expression — so codegen
// emits exactly the plain-lmut function (same exclusive mutable ref, scalar return).
// The zero-overhead equivalence lmut ≡ mutable& is already pinned by goldens.
//
// Enforced here (what makes the manifest TRUE rather than decorative):
//  1. Each `name: lmut T` return field must name an `lmut` parameter.
//  2. Every return statement must be a literal tuple of the full declared arity,
//     with the threaded slots being exactly the bare parameter name in tail
//     position. Anything else in a threaded slot is an error.
//
// The validated slots are retained on FuncDecl.LmutThreadSlots for the semantic
// layer's docs/120 §3 call-site rules (rebind form, must-use).
func (p *Parser) applyDeclaredLmutThreading(fn *ast.FuncDecl) {
	tuple, ok := fn.ReturnType.(*ast.TupleTypeExpr)
	if !ok {
		return
	}
	var kept []ast.TupleTypeField
	var slots []ast.LmutThreadSlot
	for i, f := range tuple.Fields {
		mt, isMut := f.Type.(*ast.MutableType)
		if !isMut || !mt.Linear {
			kept = append(kept, f)
			continue
		}
		paramIndex := -1
		for j, prm := range fn.Params {
			if prm.Name == f.Name {
				paramIndex = j
				break
			}
		}
		if paramIndex < 0 {
			p.errorAt(f.Position, "declared lmut return slot %q does not name a parameter", f.Name)
			return
		}
		pmt, pIsMut := fn.Params[paramIndex].Type.(*ast.MutableType)
		if !pIsMut || !pmt.Linear {
			p.errorAt(f.Position, "declared lmut return slot %q must thread an `lmut` parameter, but %q is not lmut", f.Name, f.Name)
			return
		}
		slots = append(slots, ast.LmutThreadSlot{TupleIndex: i, ParamIndex: paramIndex, ParamName: f.Name})
	}
	if len(slots) == 0 {
		return
	}

	if !p.rewriteLmutThreadReturns(fn.Body, len(tuple.Fields), slots) {
		return
	}

	switch len(kept) {
	case 0:
		fn.ReturnType = nil
	case 1:
		fn.ReturnType = kept[0].Type
	default:
		fn.ReturnType = &ast.TupleTypeExpr{Position: tuple.Position, Fields: kept}
	}
	fn.LmutThreadSlots = slots
}

// rewriteLmutThreadReturns walks every return statement reachable from body,
// validates it against the declared manifest, and erases the threaded slots from
// its tuple. The walk is a generic deep reflection traversal — a return can hide
// inside ANY statement container (if/match/can/loops) and also inside statement
// bodies that hang off EXPRESSIONS (a match-expression arm, a value block): those
// returns exit the enclosing function, so they carry the threading obligation too.
// The only stops are lambda and nested function nodes, whose returns belong to the
// lambda/function itself.
func (p *Parser) rewriteLmutThreadReturns(body []ast.Stmt, declaredArity int, slots []ast.LmutThreadSlot) bool {
	ok := true
	var walk func(v reflect.Value)

	isThreadSlot := func(i int) (ast.LmutThreadSlot, bool) {
		for _, s := range slots {
			if s.TupleIndex == i {
				return s, true
			}
		}
		return ast.LmutThreadSlot{}, false
	}

	// A multi-pattern match arm (`"public" | "private": …`) shares one body across
	// its alternatives, so the walk can reach the same return twice — rewrite once.
	// rewriteLeafTuple validates one concrete return tuple against the manifest and
	// returns it with the threaded slots erased (bare expr / smaller tuple / nil).
	rewriteLeafTuple := func(value ast.Expr, pos lexer.Pos) ast.Expr {
		var elems []ast.Expr
		tuplePos := pos
		if tup, isTuple := value.(*ast.TupleExpr); isTuple {
			elems = tup.Elems
			tuplePos = tup.Position
		} else if declaredArity == 1 && value != nil {
			// A 1-slot declaration returns a bare expression (a 1-tuple has no
			// literal spelling) — treat it as the single slot.
			elems = []ast.Expr{value}
		}
		if len(elems) != declaredArity {
			p.errorAt(pos, "a function with declared lmut threading must return a literal tuple of all %d declared slots (threaded params in their declared positions)", declaredArity)
			ok = false
			return value
		}
		var keptElems []ast.Expr
		for i, el := range elems {
			slot, threaded := isThreadSlot(i)
			if !threaded {
				keptElems = append(keptElems, el)
				continue
			}
			ident, isIdent := el.(*ast.Ident)
			if !isIdent || ident.Name != slot.ParamName {
				p.errorAt(el.Pos(), "threaded return slot %d must be exactly the lmut parameter %q", i+1, slot.ParamName)
				ok = false
				return value
			}
		}
		switch len(keptElems) {
		case 0:
			return nil
		case 1:
			return keptElems[0]
		default:
			return &ast.TupleExpr{Position: tuplePos, Elems: keptElems}
		}
	}

	// rewriteReturnValue descends through the return value's compound value-expression
	// shape — a value-`if` (TernaryExpr), a value block (ExprBlock), a value `match`
	// (MatchExpr) — so each yielding LEAF tuple carries the threading manifest and is
	// erased. This is what lets a `return if cond: A, p else: …; B, p` thread through
	// its branches (docs/120 §10 / docs/119 §4) rather than only a flat literal tuple.
	var rewriteReturnValue func(value ast.Expr, pos lexer.Pos) ast.Expr
	rewriteReturnValue = func(value ast.Expr, pos lexer.Pos) ast.Expr {
		switch e := value.(type) {
		case *ast.TernaryExpr:
			e.Value = rewriteReturnValue(e.Value, e.Value.Pos())
			e.Alt = rewriteReturnValue(e.Alt, e.Alt.Pos())
			return e
		case *ast.ExprBlock:
			if e.Value != nil {
				e.Value = rewriteReturnValue(e.Value, e.Value.Pos())
			}
			return e
		case *ast.MatchExpr:
			for ai := range e.Arms {
				body := e.Arms[ai].Body
				if len(body) == 0 {
					continue
				}
				if tail, isExpr := body[len(body)-1].(*ast.ExprStmt); isExpr && tail.Expr != nil {
					tail.Expr = rewriteReturnValue(tail.Expr, tail.Expr.Pos())
				}
			}
			return e
		default:
			return rewriteLeafTuple(value, pos)
		}
	}

	rewritten := map[*ast.ReturnStmt]bool{}
	rewriteReturn := func(ret *ast.ReturnStmt) {
		if rewritten[ret] {
			return
		}
		rewritten[ret] = true
		ret.Value = rewriteReturnValue(ret.Value, ret.Position)
	}

	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			walk(v.Elem())
		case reflect.Ptr:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.ReturnStmt:
				rewriteReturn(n)
			case *ast.LambdaExpr, *ast.FuncDecl:
				// A nested function's returns are its own.
			default:
				walk(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice:
			for j := 0; j < v.Len(); j++ {
				walk(v.Index(j))
			}
		}
	}

	for _, s := range body {
		walk(reflect.ValueOf(s))
	}
	return ok
}
