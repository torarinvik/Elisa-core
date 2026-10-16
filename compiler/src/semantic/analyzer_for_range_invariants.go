package semantic

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Inductive invariants for counted range loops. `for i in S..<E: body` with leading `invariant`
// clauses runs exactly like
//
//	i: mutable = S
//	while i < E:
//	    body
//	    i <- i + 1
//
// provided (a) the step is the default 1 and the range ascending (`..<`; `..=` arrives as `..<`),
// (b) the body has no `continue` of this loop (the for loop still increments on `continue`; the
// rewritten loop would not), (c) E is side-effect free and reads nothing the body writes (the for
// loop evaluates E once; the rewritten loop re-reads it every iteration, so both agree only then),
// and (d) the loop variable is immutable in the body (it always is). Under those conditions the
// while-loop invariant prover (establishment on the pre-loop facts with i == S, preservation over the
// body plus the increment) proves exactly the for loop's invariants. The loop variable is modeled in
// a private scope that is discarded after the proof.
//
// Proven invariants are handed to the in-place checks in the real body (they hold at every
// iteration head). Exit facts: when every invariant is inductive on a straight-line body and S <= E
// is provable at entry, the loop ends with i == E, so `inv[i := E]` holds after the loop.
func (a *Analyzer) proveForRangeInvariants(stmt *ast.ForStmt, loopType Type) (proven []*ast.ContractStmt, exitFacts []ast.Expr) {
	if a == nil || stmt == nil || a.currentScope == nil || stmt.Reverse || stmt.Op != lexer.TOKEN_RANGE_LT {
		return nil, nil
	}
	if stmt.Step != nil {
		if v, ok := a.constIntValue(stmt.Step); !ok || v != 1 {
			return nil, nil
		}
	}
	if !IsIntegralType(loopType) || stmt.Start == nil || stmt.End == nil {
		return nil, nil
	}
	invs := leadingInvariants(stmt.Body)
	if len(invs) == 0 || loopBodyContinues(stmt.Body) {
		return nil, nil
	}
	if !sideEffectFreeExpr(stmt.End, 0) || !sideEffectFreeExpr(stmt.Start, 0) {
		return nil, nil
	}
	written := loopWrittenRoots(stmt.Body)
	if exprReadsAnyRoot(stmt.End, written) || written[stmt.Name] {
		return nil, nil
	}
	pos := stmt.Pos()
	ident := func() *ast.Ident {
		id := &ast.Ident{Position: pos, Name: stmt.Name}
		a.exprTypes[id] = loopType
		return id
	}
	decl := &ast.VarDeclStmt{Position: pos, Name: stmt.Name, Mutable: true, Value: stmt.Start}
	scope := NewScope(a.currentScope)
	scope.Define(&Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: loopType, Node: decl, Mutable: true})
	entry := &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_EQEQ, Left: ident(), Right: stmt.Start}
	a.exprTypes[entry] = &BuiltinType{Name: "bool"}
	a.recordSMTAssertFactInScope(scope, entry)
	cond := &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LT, Left: ident(), Right: stmt.End}
	a.exprTypes[cond] = &BuiltinType{Name: "bool"}
	one := &ast.IntLit{Position: pos, Value: "1"}
	a.exprTypes[one] = loopType
	next := &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: ident(), Right: one}
	a.exprTypes[next] = loopType
	body := append(append([]ast.Stmt(nil), stmt.Body...), &ast.AssignStmt{Position: pos, Target: ident(), Value: next})
	synth := &ast.WhileStmt{Position: pos, Cond: cond, Body: body}

	saved := a.currentScope
	a.currentScope = scope
	proven, exitSound := a.proveLoopInvariants(synth)
	a.currentScope = saved
	// A range-loop invariant the inductive proof could not certify keeps its previous treatment (the
	// in-place check at the body head); drop the deferred inductive lint the while prover queued.
	for _, inv := range invs {
		delete(a.pendingLoopInvariantLints, inv)
	}
	if !exitSound || len(proven) != len(invs) {
		return proven, nil
	}
	// The loop ends with i == E only when it starts at or below E.
	le := &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LTEQ, Left: stmt.Start, Right: stmt.End}
	a.exprTypes[le] = &BuiltinType{Name: "bool"}
	if ok, _ := a.trySMTProveRequires(le, nil); !ok {
		return proven, nil
	}
	for _, inv := range proven {
		fact, ok := a.substituteTypedIdents(inv.Cond, map[string]ast.Expr{stmt.Name: stmt.End}, 0)
		if !ok {
			continue
		}
		exitFacts = append(exitFacts, fact)
	}
	return proven, exitFacts
}

// loopBodyContinues reports a `continue` that targets this loop (one not nested in an inner loop).
func loopBodyContinues(body []ast.Stmt) bool {
	found := false
	var walk func(v reflect.Value, depth int)
	walk = func(v reflect.Value, depth int) {
		if found || !v.IsValid() || depth > 200 {
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch v.Interface().(type) {
			case *ast.ContinueStmt:
				found = true
				return
			case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt, *ast.ParallelForStmt, *ast.LambdaExpr:
				return
			}
			walk(v.Elem(), depth+1)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i), depth+1)
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), depth+1)
			}
		}
	}
	for _, s := range body {
		walk(reflect.ValueOf(s), 0)
	}
	return found
}
