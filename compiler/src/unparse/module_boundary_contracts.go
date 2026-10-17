package unparse

import (
	"elisacore/src/ast"
	"strings"
)

func formatBoundaryValueContracts(requires []ast.Expr, ensures []ast.Expr) string {
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
