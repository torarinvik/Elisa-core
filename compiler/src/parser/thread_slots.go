package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/120 §6 — thread slots in multi-place assignment: the manifest as dataflow.
//
//	parser, out <-
//	    if d_opt is d:
//	        parser, out.push(d)
//	    else:
//	        parser.advance(), out
//
// Slot i of the tuple is a THREAD SLOT when target i is a bare binding N and the slot
// expression is N itself or a (possibly chained) method call whose receiver root is N.
// A thread slot ERASES: a mutating call "yields" its receiver notationally (the §2
// erased-return model extended from function returns to expression slots), so the
// effect simply executes in place and no assignment is emitted — zero overhead with
// value-semantics reading. Slots whose root is not their target stay VALUE slots and
// keep the §1 simultaneous-assignment semantics.
//
// Rules enforced here:
//   - Arm consistency: every yield tuple of the RHS (each value-if branch) must
//     classify slot i the same way — threading in one arm and yielding an unrelated
//     value in another is an error.
//   - No cross-reads: a VALUE slot may not be rooted at a binding some thread slot
//     mutates — thread effects execute where they sit, so reading the threaded
//     binding from a sibling slot would be order-ambiguous. Bind it before the `<-`.
//
// E4 licensing: hoisted thread effects live as statements inside the RHS's branch
// blocks, which are value blocks — the thread targets are added to those blocks'
// Captures, making "the target list is the capture header" literal (docs/119 E10
// still validates each name is a mutable / mutable-ref binding).

// threadLeaf is one yield site of the RHS: its tuple, the enclosing branch block (nil
// when the tuple is bare), and a setter that replaces the leaf in its parent.
type threadLeaf struct {
	tuple   *ast.TupleExpr
	block   *ast.ExprBlock
	replace func(ast.Expr)
}

// desugarThreadSlots classifies the RHS's slots against the targets and, when any slot
// threads, rewrites the construct. handled=false means no slot threads and the §1 pure
// path applies unchanged.
func (p *Parser) desugarThreadSlots(pos lexer.Pos, places []ast.Expr, value ast.Expr) (ast.Stmt, bool) {
	targetNames := make([]string, len(places))
	anyBare := false
	for i, pl := range places {
		if id, ok := pl.(*ast.Ident); ok {
			targetNames[i] = id.Name
			anyBare = true
		}
	}
	if !anyBare {
		return nil, false
	}

	leaves, enumerable := collectThreadLeaves(value)
	if !enumerable || len(leaves) == 0 {
		return nil, false
	}
	for _, leaf := range leaves {
		if len(leaf.tuple.Elems) != len(places) {
			return nil, false
		}
	}

	// Classify each slot across every leaf. Per leaf a slot is a THREAD (a mutating
	// call rooted at the target), a VALUE (anything else), or NEUTRAL (the bare target
	// binding itself — thread-erase and self-assign are the same no-op, so it is
	// compatible with either aggregate). Mixing thread and value leaves is an error.
	isThread := make([]bool, len(places))
	anyThread := false
	for i := range places {
		if targetNames[i] == "" {
			continue
		}
		threads, values := 0, 0
		for _, leaf := range leaves {
			switch classifySlot(leaf.tuple.Elems[i], targetNames[i]) {
			case slotThread:
				threads++
			case slotValue:
				values++
			}
		}
		switch {
		case threads > 0 && values > 0:
			p.errorAt(places[i].Pos(), "slot %d threads %q in some branches but yields an unrelated value in others; every branch must classify a slot the same way (docs/120 §6)", i+1, targetNames[i])
			return &ast.ExprStmt{Position: pos, Expr: value}, true
		case threads > 0:
			isThread[i] = true
			anyThread = true
		}
	}
	if !anyThread {
		return nil, false
	}

	threadNames := make([]string, 0, len(places))
	for i := range places {
		if isThread[i] {
			threadNames = append(threadNames, targetNames[i])
		}
	}

	// No cross-reads: a value slot rooted at a threaded binding is order-ambiguous.
	for _, leaf := range leaves {
		for i, el := range leaf.tuple.Elems {
			if isThread[i] {
				continue
			}
			if root, ok := exprRootIdentName(el); ok {
				for _, tn := range threadNames {
					if root == tn {
						p.errorAt(el.Pos(), "value slot reads %q, which a thread slot of this assignment mutates; bind the value before the `<-` (docs/120 §6)", tn)
						return &ast.ExprStmt{Position: pos, Expr: value}, true
					}
				}
			}
		}
	}

	// hoistsOf: the thread effects of one leaf, in slot order (bare bindings are pure
	// no-op threads and hoist nothing).
	hoistsOf := func(tuple *ast.TupleExpr) []ast.Stmt {
		var out []ast.Stmt
		for i, el := range tuple.Elems {
			if !isThread[i] {
				continue
			}
			if _, isBare := el.(*ast.Ident); !isBare {
				markThreadEffect(el)
				out = append(out, &ast.ExprStmt{Position: el.Pos(), Expr: el})
			}
		}
		return out
	}

	var keptPlaces []ast.Expr
	for i, pl := range places {
		if !isThread[i] {
			keptPlaces = append(keptPlaces, pl)
		}
	}

	// All slots thread: nothing remains to bind — the construct is plain statements
	// (a statement-if for a value-if RHS), and E4 no longer applies at all.
	if len(keptPlaces) == 0 {
		stmts := threadOnlyStmts(value, hoistsOf)
		if len(stmts) == 0 {
			p.errorAt(pos, "thread-only assignment has no effects to run (docs/120 §6)")
			return &ast.ExprStmt{Position: pos, Expr: &ast.NullLit{Position: pos}}, true
		}
		p.pendingStmts = append(p.pendingStmts, stmts[1:]...)
		return stmts[0], true
	}

	// Mixed form: rewrite each leaf — hoist thread effects into the branch block
	// (licensed via Captures), keep value slots as the leaf's yield.
	for _, leaf := range leaves {
		hoisted := hoistsOf(leaf.tuple)
		var keptElems []ast.Expr
		for i, el := range leaf.tuple.Elems {
			if !isThread[i] {
				keptElems = append(keptElems, el)
			}
		}
		var newLeaf ast.Expr
		if len(keptElems) == 1 {
			newLeaf = keptElems[0]
		} else {
			newLeaf = &ast.TupleExpr{Position: leaf.tuple.Position, Elems: keptElems}
		}
		if leaf.block != nil {
			leaf.block.Stmts = append(leaf.block.Stmts, hoisted...)
			leaf.block.Captures = appendMissingNames(leaf.block.Captures, threadNames)
			leaf.block.Value = newLeaf
		} else if len(hoisted) > 0 {
			leaf.replace(&ast.ExprBlock{Position: leaf.tuple.Position, Stmts: hoisted, Value: newLeaf, Captures: append([]string(nil), threadNames...)})
		} else {
			leaf.replace(newLeaf)
		}
	}
	return p.buildPureMultiPlaceAssign(pos, keptPlaces, value), true
}

// docs/120 — single-place thread assignment: §6 thread slots extended from multi-place
// tuples to the single-target form `x <- <branchy RHS>`. This is the total-mutation
// value-if spelling of the linear model:
//
//	lexer <-
//	    if width > 1:
//	        lexer.advance_chars(width)   # thread: effect in place, "yields" lexer
//	    elif width == 1:
//	        lexer.advance_char()
//	    else:
//	        lexer                        # neutral: yields lexer unchanged (no-op)
//
// Every leaf of the RHS must account for x: a mutating call rooted at x THREADS
// (implicitly yielding its receiver — the compile-time-only return of an lmut
// mutation), and the bare binding is NEUTRAL. The whole construct then erases to a
// plain statement-if over the in-place mutations — zero overhead; the notation exists
// so the dataflow is total and visible on every path. Mixing a thread leaf with an
// unrelated-value leaf is the §6 arm-consistency error. An RHS with no thread leaf
// is a pure §1 value assignment and is left untouched (handled=false).
//
// Only branchy RHS shapes (value-if / expression block) are intercepted here: a bare
// call RHS (`x <- x.f()`) stays the §8 arg-manifest, whose semantic validation checks
// the callee's void return — the parser cannot see return types, so it must not guess.
func (p *Parser) desugarSingleThreadAssign(target ast.Expr, value ast.Expr) (ast.Stmt, bool) {
	id, ok := target.(*ast.Ident)
	if !ok {
		return nil, false
	}
	switch value.(type) {
	case *ast.TernaryExpr, *ast.ExprBlock:
	default:
		return nil, false
	}
	threads, values, enumerable := classifySingleLeaves(value, id.Name)
	if !enumerable || threads == 0 {
		return nil, false
	}
	if values > 0 {
		p.errorAt(target.Pos(), "some branches thread %q but others yield an unrelated value; every branch must either mutate %q in place or yield it unchanged (docs/120 §6)", id.Name, id.Name)
		return &ast.ExprStmt{Position: target.Pos(), Expr: value}, true
	}
	stmts := singleThreadStmts(value, id.Name)
	if len(stmts) == 0 {
		// All leaves neutral is threads==0 above; reaching here means effects exist.
		return nil, false
	}
	p.pendingStmts = append(p.pendingStmts, stmts[1:]...)
	return stmts[0], true
}

// classifySingleLeaves walks the single-place RHS's branch structure and counts leaf
// classifications against the target name. enumerable=false for a shape the walk
// cannot decompose (the pure path applies).
func classifySingleLeaves(value ast.Expr, name string) (threads, values int, enumerable bool) {
	switch n := value.(type) {
	case *ast.TernaryExpr:
		t1, v1, ok1 := classifySingleLeaves(n.Value, name)
		t2, v2, ok2 := classifySingleLeaves(n.Alt, name)
		return t1 + t2, v1 + v2, ok1 && ok2
	case *ast.ExprBlock:
		if n.Value == nil {
			return 0, 0, false
		}
		return classifySingleLeaves(n.Value, name)
	default:
		switch classifySlot(value, name) {
		case slotThread:
			return 1, 0, true
		case slotNeutral:
			return 0, 0, true
		default:
			return 0, 1, true
		}
	}
}

// singleThreadStmts converts the all-thread/neutral RHS into plain statements: a
// value-if becomes a statement if, an expression block contributes its pre-tail
// statements, a thread leaf becomes its bare (marked) mutating call, and a neutral
// leaf (the bare binding) erases to nothing.
func singleThreadStmts(value ast.Expr, name string) []ast.Stmt {
	switch n := value.(type) {
	case *ast.TernaryExpr:
		thenStmts := singleThreadStmts(n.Value, name)
		elseStmts := singleThreadStmts(n.Alt, name)
		if len(thenStmts) == 0 {
			thenStmts = []ast.Stmt{&ast.PassStmt{Position: n.Position}}
		}
		return []ast.Stmt{&ast.IfStmt{Position: n.Position, Cond: n.Cond, Then: thenStmts, Else: elseStmts}}
	case *ast.ExprBlock:
		stmts := append([]ast.Stmt(nil), n.Stmts...)
		return append(stmts, singleThreadStmts(n.Value, name)...)
	default:
		if id, ok := value.(*ast.Ident); ok && id.Name == name {
			return nil // neutral: yields the binding unchanged — erases
		}
		markThreadEffect(value)
		return []ast.Stmt{&ast.ExprStmt{Position: value.Pos(), Expr: value}}
	}
}

// threadOnlyStmts converts an all-slots-thread RHS into plain statements: a value-if
// becomes a statement if whose branch bodies are the branches' pre-tail statements
// plus their hoisted thread effects.
func threadOnlyStmts(value ast.Expr, hoistsOf func(*ast.TupleExpr) []ast.Stmt) []ast.Stmt {
	switch n := value.(type) {
	case *ast.TupleExpr:
		return hoistsOf(n)
	case *ast.ExprBlock:
		// The block's pre-tail statements execute, then whatever its value position
		// contributes (a tuple's hoisted effects, or a nested value-if's statement form).
		stmts := append([]ast.Stmt(nil), n.Stmts...)
		return append(stmts, threadOnlyStmts(n.Value, hoistsOf)...)
	case *ast.TernaryExpr:
		ifStmt := &ast.IfStmt{
			Position: n.Position,
			Cond:     n.Cond,
			Then:     threadOnlyStmts(n.Value, hoistsOf),
			Else:     threadOnlyStmts(n.Alt, hoistsOf),
		}
		return []ast.Stmt{ifStmt}
	}
	return nil
}

// markThreadEffect flags every call in a thread-slot expression's receiver chain as a
// §6 thread effect, so the §7 strict tier treats it as a manifest point (a chained
// `p.advance().skip_spaces()` threads through each in-place mutation).
func markThreadEffect(e ast.Expr) {
	for {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return
		}
		call.LmutThreadEffect = true
		field, ok := call.Func.(*ast.FieldExpr)
		if !ok {
			return
		}
		e = field.Object
	}
}

type slotClass int

const (
	slotValue slotClass = iota
	slotThread
	slotNeutral
)

// classifySlot: a mutating call rooted at the target THREADS; the bare target binding
// is NEUTRAL (thread-erase ≡ self-assign); anything else is a VALUE.
func classifySlot(e ast.Expr, name string) slotClass {
	if id, ok := e.(*ast.Ident); ok && id.Name == name {
		return slotNeutral
	}
	if call, ok := e.(*ast.CallExpr); ok {
		if root, ok := exprRootIdentName(call); ok && root == name {
			return slotThread
		}
	}
	return slotValue
}

// exprRootIdentName walks a receiver/place chain (field access, indexing, method
// calls, parens, address-of) to its root identifier.
func exprRootIdentName(e ast.Expr) (string, bool) {
	for {
		switch n := e.(type) {
		case *ast.Ident:
			return n.Name, true
		case *ast.ParenExpr:
			e = n.Inner
		case *ast.AddrOfExpr:
			e = n.Operand
		case *ast.FieldExpr:
			e = n.Object
		case *ast.IndexExpr:
			e = n.Object
		case *ast.CallExpr:
			field, ok := n.Func.(*ast.FieldExpr)
			if !ok {
				return "", false
			}
			e = field.Object
		default:
			return "", false
		}
	}
}

// collectThreadLeaves enumerates the RHS's yield tuples with setters. Handles the §1
// RHS shapes: a bare tuple, a value-if (TernaryExpr, elif chains nested in Alt), and
// branch ExprBlocks. Any other shape is not enumerable (the pure path applies).
func collectThreadLeaves(value ast.Expr) ([]*threadLeaf, bool) {
	var leaves []*threadLeaf
	var walk func(e ast.Expr, block *ast.ExprBlock, replace func(ast.Expr)) bool
	walk = func(e ast.Expr, block *ast.ExprBlock, replace func(ast.Expr)) bool {
		switch n := e.(type) {
		case *ast.TupleExpr:
			leaves = append(leaves, &threadLeaf{tuple: n, block: block, replace: replace})
			return true
		case *ast.ExprBlock:
			return walk(n.Value, n, func(nv ast.Expr) { n.Value = nv })
		case *ast.TernaryExpr:
			if !walk(n.Value, nil, func(nv ast.Expr) { n.Value = nv }) {
				return false
			}
			return walk(n.Alt, nil, func(nv ast.Expr) { n.Alt = nv })
		default:
			return false
		}
	}
	if !walk(value, nil, func(ast.Expr) {}) {
		return nil, false
	}
	return leaves, true
}

// appendMissingNames appends the names not already present in dst.
func appendMissingNames(dst []string, names []string) []string {
	for _, n := range names {
		found := false
		for _, d := range dst {
			if d == n {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, n)
		}
	}
	return dst
}
