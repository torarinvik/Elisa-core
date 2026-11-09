//go:build cgo

package backend

import (
	"elisacore/src/ast"
)

func matchArmsReadMatchedValueField(name string, arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if stmtsReadMatchedValueField(name, arm.Body) {
			return true
		}
	}
	return false
}
func stmtsReadMatchedValueField(name string, stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		if stmtReadsMatchedValueField(name, stmt) {
			return true
		}
	}
	return false
}
func stmtReadsMatchedValueField(name string, stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		return exprReadsMatchedValueField(name, n.Target) || exprReadsMatchedValueField(name, n.Value)
	case *ast.AugAssignStmt:
		return exprReadsMatchedValueField(name, n.Target) || exprReadsMatchedValueField(name, n.Value)
	case *ast.AsRefAssignStmt:
		return exprReadsMatchedValueField(name, n.Target) || exprReadsMatchedValueField(name, n.Value)
	case *ast.VarDeclStmt:
		return exprReadsMatchedValueField(name, n.Value)
	case *ast.MoveBindStmt:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Store)
	case *ast.DeferStmt:
		return stmtsReadMatchedValueField(name, n.Body)
	case *ast.ReturnStmt:
		return exprReadsMatchedValueField(name, n.Value)
	case *ast.IfStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Then) || stmtsReadMatchedValueField(name, n.Else) || elifsReadMatchedValueField(name, n.Elifs)
	case *ast.WhileStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.ForStmt:
		return exprReadsMatchedValueField(name, n.Start) || exprReadsMatchedValueField(name, n.End) || exprReadsMatchedValueField(name, n.Step) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.IterForStmt:
		return exprReadsMatchedValueField(name, n.Source) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.ParallelForStmt:
		return exprReadsMatchedValueField(name, n.Source) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.MatchStmt:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Store) || matchArmsReadMatchedValueField(name, n.Arms)
	case *ast.InStoreStmt:
		return exprReadsMatchedValueField(name, n.Store) || stmtsReadMatchedValueField(name, n.Body)
	case *ast.PanicStmt:
		return exprReadsMatchedValueField(name, n.Message)
	case *ast.ExprStmt:
		return exprReadsMatchedValueField(name, n.Expr)
	case *ast.StaticIfStmt:
		return exprReadsMatchedValueField(name, n.Cond) || stmtsReadMatchedValueField(name, n.Then) || stmtsReadMatchedValueField(name, n.Else) || staticElifsReadMatchedValueField(name, n.Elifs)
	case *ast.StaticErrorStmt:
		return exprReadsMatchedValueField(name, n.Message)
	case *ast.StaticAssertStmt:
		return exprReadsMatchedValueField(name, n.Cond) || (n.Message != nil && exprReadsMatchedValueField(name, n.Message))
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			if exprReadsMatchedValueField(name, item.Cond) || (item.Message != nil && exprReadsMatchedValueField(name, item.Message)) {
				return true
			}
		}
		return false
	case *ast.StaticBlockStmt:
		return stmtsReadMatchedValueField(name, n.Body)
	case *ast.DiscardStmt:
		return exprReadsMatchedValueField(name, n.Value)
	default:
		return false
	}
}
func elifsReadMatchedValueField(name string, elifs []ast.ElifClause) bool {
	for _, elif := range elifs {
		if exprReadsMatchedValueField(name, elif.Cond) || stmtsReadMatchedValueField(name, elif.Body) {
			return true
		}
	}
	return false
}
func staticElifsReadMatchedValueField(name string, elifs []ast.StaticElifClause) bool {
	for _, elif := range elifs {
		if exprReadsMatchedValueField(name, elif.Cond) || stmtsReadMatchedValueField(name, elif.Body) {
			return true
		}
	}
	return false
}
func exprReadsMatchedValueField(name string, expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		return exprReadsMatchedValueField(name, n.Left) || exprReadsMatchedValueField(name, n.Right)
	case *ast.UnaryExpr:
		return exprReadsMatchedValueField(name, n.Operand)
	case *ast.CallExpr:
		if exprReadsMatchedValueField(name, n.Func) {
			return true
		}
		if exprReadsMatchedValueField(name, n.SafeReceiver) {
			return true
		}
		for _, arg := range n.Args {
			if exprReadsMatchedValueField(name, arg) {
				return true
			}
		}
		return false
	case *ast.FieldExpr:
		if rootName, ok := fieldRootIdentName(n.Object); ok && rootName == name {
			return true
		}
		return exprReadsMatchedValueField(name, n.Object)
	case *ast.IndexExpr:
		return exprReadsMatchedValueField(name, n.Object) || exprReadsMatchedValueField(name, n.Index) || exprReadsMatchedValueField(name, n.Fallback)
	case *ast.SliceExpr:
		return exprReadsMatchedValueField(name, n.Object) || exprReadsMatchedValueField(name, n.Start) || exprReadsMatchedValueField(name, n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if exprReadsMatchedValueField(name, elem) {
				return true
			}
		}
		return false
	case *ast.CastExpr:
		return exprReadsMatchedValueField(name, n.Operand)
	case *ast.TernaryExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Cond) || exprReadsMatchedValueField(name, n.Alt)
	case *ast.AddrOfExpr:
		return exprReadsMatchedValueField(name, n.Operand)
	case *ast.MoveExpr:
		return exprReadsMatchedValueField(name, n.Operand)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			if exprReadsMatchedValueField(name, arg) {
				return true
			}
		}
		return false
	case *ast.ParenExpr:
		return exprReadsMatchedValueField(name, n.Inner)
	case *ast.RaiseExpr:
		return exprReadsMatchedValueField(name, n.Error)
	case *ast.TryExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Fallback)
	case *ast.CatchExpr:
		if exprReadsMatchedValueField(name, n.Value) {
			return true
		}
		if stmtsReadMatchedValueField(name, n.Success.Body) {
			return true
		}
		for _, arm := range n.Arms {
			if stmtsReadMatchedValueField(name, arm.Body) {
				return true
			}
		}
		return false
	case *ast.UnwrapElseExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Fallback)
	case *ast.GetExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Fallback)
	case *ast.OptionalBindExpr:
		return exprReadsMatchedValueField(name, n.Value)
	case *ast.AllocExpr:
		return exprReadsMatchedValueField(name, n.Owner) || exprReadsMatchedValueField(name, n.Value)
	case *ast.MatchExpr:
		return exprReadsMatchedValueField(name, n.Value) || exprReadsMatchedValueField(name, n.Store) || matchArmsReadMatchedValueField(name, n.Arms)
	case *ast.MachineFromExpr:
		return exprReadsMatchedValueField(name, n.Lowered)
	case *ast.FoldExpr:
		if exprReadsMatchedValueField(name, n.Value) {
			return true
		}
		for _, arm := range n.Arms {
			if exprReadsMatchedValueField(name, arm.Guard) {
				return true
			}
			if stmtsReadMatchedValueField(name, arm.Body) {
				return true
			}
		}
		return false
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			if exprReadsMatchedValueField(name, target) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
func fieldRootIdentName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		return fieldRootIdentName(n.Object)
	case *ast.ParenExpr:
		return fieldRootIdentName(n.Inner)
	case *ast.CastExpr:
		return fieldRootIdentName(n.Operand)
	default:
		return "", false
	}
}
func matchPatternNeedsPayloadDecode(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case *ast.MatchVariantPattern:
		if len(p.Args) > 0 {
			return true
		}
		for _, arg := range p.Args {
			if matchPatternNeedsPayloadDecode(arg.Pattern) {
				return true
			}
		}
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			if matchPatternNeedsPayloadDecode(option) {
				return true
			}
		}
	}
	return false
}
