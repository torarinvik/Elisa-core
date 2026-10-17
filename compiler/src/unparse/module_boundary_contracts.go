package unparse

import (
	"elisacore/src/ast"
	"strings"
)

func formatBoundaryValueContracts(requires []ast.Expr, uses []*ast.ContractStmt, ensures []ast.Expr) string {
	line := ""
	appendConditions := func(keyword string, expressions []ast.Expr) {
		if len(expressions) == 0 {
			return
		}
		parts := make([]string, len(expressions))
		for i, e := range expressions {
			parts[i] = formatExpr(e)
		}
		line += " " + keyword + " " + strings.Join(parts, ", ")
	}
	appendConditions("requires", requires)
	for _, used := range uses {
		line += " uses " + used.UsesName
		if len(used.UsesTypeArgs) > 0 {
			parts := make([]string, len(used.UsesTypeArgs))
			for i, t := range used.UsesTypeArgs {
				parts[i] = formatTypeExpr(t)
			}
			line += "[" + strings.Join(parts, ", ") + "]"
		}
		parts := make([]string, len(used.UsesArgs))
		for i, arg := range used.UsesArgs {
			parts[i] = formatExpr(arg)
		}
		line += "(" + strings.Join(parts, ", ") + ")"
	}
	appendConditions("ensure", ensures)
	return line
}

func formatBoundaryFrames(changes, preserves []ast.EnsuresPath) string {
	line := ""
	for _, clause := range []struct {
		keyword string
		paths   []ast.EnsuresPath
	}{{"changes", changes}, {"preserves", preserves}} {
		if len(clause.paths) == 0 {
			continue
		}
		parts := make([]string, len(clause.paths))
		for i, path := range clause.paths {
			parts[i] = formatEnsuresPath(path)
		}
		line += " " + clause.keyword + " " + strings.Join(parts, ", ")
	}
	return line
}

// Bodiless protocol and impl methods carry value contracts in an indented block.
func (f *formatter) writeBoundaryContracts(level int, requires, ensures []ast.Expr) {
	for _, e := range requires {
		f.writeLine(level, "requires "+formatExpr(e))
	}
	for _, e := range ensures {
		f.writeLine(level, "ensure "+formatExpr(e))
	}
}

func formatBoundaryFulfills(clauses []ast.FulfillsClause) string {
	if len(clauses) == 0 {
		return ""
	}
	parts := make([]string, len(clauses))
	for i, clause := range clauses {
		parts[i] = clause.Law
		if clause.Param != "" {
			parts[i] = clause.Param + " is " + clause.Law
		}
	}
	return " fulfills " + strings.Join(parts, ", ")
}
