package semantic

import (
	"fmt"

	"llcontext/src/ast"
)

func appendVisitArmLocals(locals map[string]bool, arm ast.VisitArm) {
	if locals == nil {
		return
	}
	if arm.BindName != "" {
		locals[arm.BindName] = true
	}
	if arm.ChildResultsName != "" {
		locals[arm.ChildResultsName] = true
	}
	for _, binding := range arm.ChildBindings {
		if binding.BindName != "" {
			locals[binding.BindName] = true
		}
	}
}

func (c *parallelForCaptureCollector) collectAssignmentTarget(expr ast.Expr, locals map[string]bool) {
	c.collectExpr(expr, locals)
	if root, ok := parallelForAssignmentRoot(expr); ok && !locals[root] {
		c.addError(fmt.Sprintf("parallel for body cannot mutate outer binding %q", root))
	}
}

func (c *parallelForCaptureCollector) collectExpr(expr ast.Expr, locals map[string]bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if locals[n.Name] {
			return
		}
		sym, ok := c.outerScope.Lookup(n.Name)
		if ok && parallelForCapturableSymbolKind(sym.Kind) {
			c.noteCapture(n.Name)
		}
	case *ast.BinaryExpr:
		c.collectExpr(n.Left, locals)
		c.collectExpr(n.Right, locals)
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.CallExpr:
		c.collectExpr(n.Func, locals)
		for _, arg := range n.Args {
			c.collectExpr(arg, locals)
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object, locals)
	case *ast.IndexExpr:
		c.collectExpr(n.Object, locals)
		c.collectExpr(n.Index, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.SliceExpr:
		c.collectExpr(n.Object, locals)
		c.collectExpr(n.Start, locals)
		c.collectExpr(n.End, locals)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem, locals)
		}
	case *ast.CastExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.TernaryExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Cond, locals)
		c.collectExpr(n.Alt, locals)
	case *ast.AddrOfExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.MoveExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.SpecializeExpr:
		c.collectExpr(n.Operand, locals)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			c.collectExpr(arg, locals)
		}
	case *ast.ParenExpr:
		c.collectExpr(n.Inner, locals)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error, locals)
	case *ast.TryExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.CatchExpr:
		c.collectExpr(n.Value, locals)
		successLocals := cloneParallelForLocals(locals)
		successLocals[n.Success.Name] = true
		for _, innerStmt := range n.Success.Body {
			c.collectStmt(innerStmt, successLocals)
		}
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Fallback, locals)
	case *ast.OptionalBindExpr:
		c.collectExpr(n.Value, locals)
	case *ast.AllocExpr:
		c.collectExpr(n.Owner, locals)
		c.collectExpr(n.Value, locals)
	case *ast.CanExpr:
		c.collectExpr(n.Expr, locals)
	case *ast.MatchExpr:
		c.collectExpr(n.Value, locals)
		c.collectExpr(n.Store, locals)
		for _, arm := range n.Arms {
			armLocals := cloneParallelForLocals(locals)
			for _, name := range parallelForMatchArmPatternNames(arm.Pattern) {
				armLocals[name] = true
			}
			for _, innerStmt := range arm.Body {
				c.collectStmt(innerStmt, armLocals)
			}
		}
	case *ast.LambdaExpr:
		lambdaLocals := cloneParallelForLocals(locals)
		for _, param := range n.Params {
			if param.Name != "" {
				lambdaLocals[param.Name] = true
			}
		}
		if n.BodyExpr != nil {
			c.collectExpr(n.BodyExpr, lambdaLocals)
		} else {
			for _, innerStmt := range n.Body {
				c.collectStmt(innerStmt, lambdaLocals)
			}
		}
	}
}

func parallelForMatchPatternNames(args []ast.MatchPatternArg) []string {
	var out []string
	for _, arg := range args {
		out = append(out, parallelForMatchArmPatternNames(arg.Pattern)...)
	}
	return out
}

func parallelForMatchArmPatternNames(pattern ast.MatchPattern) []string {
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		return []string{p.Name}
	case *ast.MatchStructPattern:
		return parallelForMatchPatternNames(p.Args)
	case *ast.MatchVariantPattern:
		return parallelForMatchPatternNames(p.Args)
	default:
		return nil
	}
}

func parallelForMoveBindNames(pattern ast.MoveBindPattern) []string {
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return []string{p.Name}
	case *ast.MoveBindStructPattern:
		var out []string
		for _, arg := range p.Args {
			if arg.Name != "" {
				out = append(out, arg.Name)
			}
		}
		return out
	case *ast.MoveBindTuplePattern:
		var out []string
		for _, arg := range p.Args {
			if arg.Name != "" {
				out = append(out, arg.Name)
			}
		}
		return out
	case *ast.MoveBindVariantPattern:
		return parallelForMatchPatternNames(p.Args)
	default:
		return nil
	}
}

func parallelForAssignmentRoot(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		return parallelForAssignmentRoot(n.Object)
	case *ast.IndexExpr:
		return parallelForAssignmentRoot(n.Object)
	case *ast.ParenExpr:
		return parallelForAssignmentRoot(n.Inner)
	case *ast.CastExpr:
		return parallelForAssignmentRoot(n.Operand)
	default:
		return "", false
	}
}
