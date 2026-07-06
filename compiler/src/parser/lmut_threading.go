package parser

import (
	"reflect"

	"elisacore/src/ast"
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

// rewriteLmutThreadReturns walks every statement list reachable from body (via
// reflection, so no container shape — if/match arms/can blocks/loops — is missed),
// validates each return against the declared manifest, and erases the threaded
// slots from its tuple. Lambda bodies are NOT entered: statement lists are only
// reachable through Stmt fields, and a lambda body hangs off an expression, which
// the walk never descends into — its returns belong to the lambda.
func (p *Parser) rewriteLmutThreadReturns(body []ast.Stmt, declaredArity int, slots []ast.LmutThreadSlot) bool {
	ok := true
	var walkStmt func(s ast.Stmt)
	var walkStructLists func(v reflect.Value)

	isThreadSlot := func(i int) (ast.LmutThreadSlot, bool) {
		for _, s := range slots {
			if s.TupleIndex == i {
				return s, true
			}
		}
		return ast.LmutThreadSlot{}, false
	}

	rewriteReturn := func(ret *ast.ReturnStmt) {
		var elems []ast.Expr
		var tuplePos = ret.Position
		if tup, isTuple := ret.Value.(*ast.TupleExpr); isTuple {
			elems = tup.Elems
			tuplePos = tup.Position
		} else if declaredArity == 1 && ret.Value != nil {
			// A 1-slot declaration returns a bare expression (a 1-tuple has no
			// literal spelling) — treat it as the single slot.
			elems = []ast.Expr{ret.Value}
		}
		if len(elems) != declaredArity {
			p.errorAt(ret.Position, "a function with declared lmut threading must return a literal tuple of all %d declared slots (threaded params in their declared positions)", declaredArity)
			ok = false
			return
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
				return
			}
		}
		switch len(keptElems) {
		case 0:
			ret.Value = nil
		case 1:
			ret.Value = keptElems[0]
		default:
			ret.Value = &ast.TupleExpr{Position: tuplePos, Elems: keptElems}
		}
	}

	// Reflection walk: from a statement struct, recurse into every []ast.Stmt field
	// and into struct-slice fields (match arms, catch arms, …) that themselves hold
	// statement lists. Expression fields are never descended into.
	walkStructLists = func(v reflect.Value) {
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return
		}
		stmtSliceType := reflect.TypeOf([]ast.Stmt(nil))
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			switch {
			case f.Type() == stmtSliceType:
				for j := 0; j < f.Len(); j++ {
					if s, isStmt := f.Index(j).Interface().(ast.Stmt); isStmt {
						walkStmt(s)
					}
				}
			case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.Struct:
				for j := 0; j < f.Len(); j++ {
					walkStructLists(f.Index(j).Addr())
				}
			}
		}
	}

	walkStmt = func(s ast.Stmt) {
		if ret, isRet := s.(*ast.ReturnStmt); isRet {
			rewriteReturn(ret)
			return
		}
		walkStructLists(reflect.ValueOf(s))
	}

	for _, s := range body {
		walkStmt(s)
	}
	return ok
}
