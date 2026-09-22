package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// docs/119 §3: an accumulator loop is an EXPRESSION. `for … |valid: bool = true| -> valid:`
// evaluates to its final accumulator, and a header accumulator is declared INSIDE the loop's
// block — so when the loop is written as a statement with more statements after it, its value
// is thrown away and nothing else holds it. elisa-engine shipped 16 validators of the shape
//
//	for i in 0..<3 |valid: bool = true| -> valid:
//	    valid <- false if values[i] <= 0
//	true
//
// which always passed (elisa-engine 4c81848). This is an error, not a lint, because the
// program is well-typed and silently wrong.
//
// Flagged: a loop expression (wrapLoopHeader's `ExprStmt(ExprBlock{decls…, loop} → yield)`)
// whose yield reads one of its own header accumulators, when something follows it in the same
// block. Every statement of a value block counts as followed — the block's tail value comes
// after it. A function's tail loop is its return value and is not a discard.
//
// NOT flagged: a yield that reads only CAPTURED outer bindings (`|table| -> table`). The loop
// writes the outer binding in place, so dropping the yield loses nothing.
//
// stage1 reports the same rule with the same sentence (check_discarded_loop_value.elisa).
func (a *Analyzer) checkDiscardedLoopValues(fn *ast.FuncDecl) {
	if fn == nil {
		return
	}
	stmtList := reflect.TypeOf([]ast.Stmt(nil))
	checkList := func(stmts []ast.Stmt, tailFollows bool) {
		for i, stmt := range stmts {
			if tailFollows || i+1 < len(stmts) {
				a.reportDiscardedLoopValue(stmt)
			}
		}
	}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if block, ok := v.Interface().(*ast.ExprBlock); ok {
				checkList(block.Stmts, block.Value != nil)
				for _, stmt := range block.Stmts {
					walk(reflect.ValueOf(stmt))
				}
				walk(reflect.ValueOf(block.Value))
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			if v.Type() == stmtList {
				checkList(v.Interface().([]ast.Stmt), false)
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(fn.Body))
}

// reportDiscardedLoopValue reports stmt when it is an accumulator loop whose yield reads one of
// its own header accumulators. The block is `[accumulator decls…, loop]` with the yield as its
// value (wrapLoopHeader); any other block shape is not a loop expression.
func (a *Analyzer) reportDiscardedLoopValue(stmt ast.Stmt) {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok || exprStmt == nil {
		return
	}
	block, ok := exprStmt.Expr.(*ast.ExprBlock)
	if !ok || block == nil || block.Value == nil || len(block.Stmts) == 0 {
		return
	}
	loop := block.Stmts[len(block.Stmts)-1]
	switch loop.(type) {
	case *ast.ForStmt, *ast.IterForStmt, *ast.WhileStmt, *ast.ParallelForStmt:
	default:
		return
	}
	for _, stmt := range block.Stmts[:len(block.Stmts)-1] {
		decl, ok := stmt.(*ast.VarDeclStmt)
		if !ok || decl == nil || !exprMentionsAny(block.Value, map[string]bool{decl.Name: true}) {
			continue
		}
		a.errorf(loop.Pos(), "accumulator loop result `%s` is discarded; make the loop the final expression of its block, bind it (`%s: T = <loop>`), or drop `-> %s` if only its effects matter", decl.Name, decl.Name, decl.Name)
		return
	}
}
