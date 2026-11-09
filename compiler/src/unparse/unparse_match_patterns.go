package unparse

import (
	"elisacore/src/ast"
	"strconv"
	"strings"
)

func formatMatchExpr(expr *ast.MatchExpr) string {
	if expr == nil {
		return "match <nil>:"
	}
	return formatMatchBody(expr.Value, expr.Store, expr.Arms)
}
func formatMatchBody(value ast.Expr, store ast.Expr, arms []ast.MatchArm) string {
	var builder strings.Builder
	builder.WriteString(formatMatchHeader("match", value, store))
	for _, arm := range arms {
		builder.WriteByte('\n')
		builder.WriteString(indentUnit)
		builder.WriteString(formatMatchPattern(arm.Pattern))
		builder.WriteString(":")
		for _, stmt := range arm.Body {
			builder.WriteByte('\n')
			stmtText := indentMultiline(FormatStmt(stmt), 2)
			builder.WriteString(strings.TrimRight(stmtText, "\n"))
		}
	}
	return builder.String()
}
func formatCatchExpr(expr *ast.CatchExpr) string {
	if expr == nil {
		return "catch <nil>:"
	}
	var builder strings.Builder
	builder.WriteString("catch ")
	builder.WriteString(formatExpr(expr.Value))
	builder.WriteString(":")
	writeArm := func(arm ast.CatchArm) {
		builder.WriteByte('\n')
		builder.WriteString(indentUnit)
		if arm.ErrorBinding {
			builder.WriteString("error ")
		}
		builder.WriteString(arm.Name)
		builder.WriteString(":")
		for _, stmt := range arm.Body {
			builder.WriteByte('\n')
			stmtText := indentMultiline(FormatStmt(stmt), 2)
			builder.WriteString(strings.TrimRight(stmtText, "\n"))
		}
	}
	writeArm(expr.Success)
	for _, arm := range expr.Arms {
		writeArm(arm)
	}
	return builder.String()
}
func formatFoldExpr(expr *ast.FoldExpr) string {
	if expr == nil {
		return "fold <nil> as <type> into <type>:"
	}
	keyword := expr.Keyword
	if keyword == "" {
		keyword = "fold"
	}
	var builder strings.Builder
	builder.WriteString(keyword)
	builder.WriteByte(' ')
	builder.WriteString(formatExpr(expr.Value))
	builder.WriteString(" as ")
	builder.WriteString(formatTypeExpr(expr.Root))
	if keyword == "rewrite" {
		if expr.RewriteDefault {
			builder.WriteString(" default")
		}
	} else {
		builder.WriteString(" into ")
		builder.WriteString(formatTypeExpr(expr.ResultType))
	}
	builder.WriteString(":")
	formatVisitArmsInto(&builder, expr.Arms)
	return builder.String()
}
func formatVisitArmsInto(builder *strings.Builder, arms []ast.VisitArm) {
	for _, arm := range arms {
		builder.WriteByte('\n')
		builder.WriteString(indentUnit)
		builder.WriteString(formatVisitArm(arm))
		builder.WriteString(":")
		for _, stmt := range arm.Body {
			builder.WriteByte('\n')
			stmtText := indentMultiline(FormatStmt(stmt), 2)
			builder.WriteString(strings.TrimRight(stmtText, "\n"))
		}
	}
}
func formatVisitArm(arm ast.VisitArm) string {
	if arm.Wildcard {
		line := "_"
		if arm.Guard != nil {
			line += " when " + formatExpr(arm.Guard)
		}
		return line
	}
	line := arm.TargetName
	if arm.BindName == "" && arm.ChildResultsName == "" && len(arm.ChildBindings) == 0 {
		if arm.Guard != nil {
			line += " when " + formatExpr(arm.Guard)
		}
		return line
	}
	line += "("
	if arm.BindName != "" {
		line += arm.BindName
	}
	if arm.ChildResultsName != "" {
		if arm.BindName != "" {
			line += ", "
		}
		line += arm.ChildResultsName
	} else if len(arm.ChildBindings) != 0 {
		for i, binding := range arm.ChildBindings {
			if arm.BindName != "" || i != 0 {
				line += ", "
			}
			line += binding.FieldName
			if binding.BindName != "" && binding.BindName != binding.FieldName {
				line += ": "
				line += binding.BindName
			}
		}
	}
	line += ")"
	if arm.Guard != nil {
		line += " when " + formatExpr(arm.Guard)
	}
	return line
}
func formatMatchPattern(pattern ast.MatchPattern) string {
	switch n := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return "_"
	case *ast.MatchBindPattern:
		if n.Binder != "" {
			return n.Name + " " + n.Binder
		}
		return n.Name
	case *ast.MatchStringLiteralPattern:
		return strconv.Quote(n.Value)
	case *ast.MatchLiteralPattern:
		if n.Pinned {
			return "^" + formatExpr(n.Value)
		}
		return formatExpr(n.Value)
	case *ast.MatchTuplePattern:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, formatMatchPattern(elem))
		}
		return strings.Join(parts, ", ")
	case *ast.MatchListPattern:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, formatMatchPattern(elem))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.MatchOrPattern:
		parts := make([]string, 0, len(n.Options))
		for _, option := range n.Options {
			parts = append(parts, formatMatchPattern(option))
		}
		return strings.Join(parts, " | ")
	case *ast.MatchRestPattern:
		return "..."
	case *ast.MatchStructPattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			part := formatMatchPatternField(arg, n.Brace)
			if part == "" {
				part = formatMatchPattern(arg.Pattern)
			}
			parts = append(parts, part)
		}
		if n.Brace {
			if n.TypeName == "" {
				return "{" + strings.Join(parts, ", ") + "}"
			}
			return ast.ModulePathSpelling(n.TypeName) + "{" + strings.Join(parts, ", ") + "}"
		}
		if len(parts) == 0 {
			return ast.ModulePathSpelling(n.TypeName) + "()"
		}
		return ast.ModulePathSpelling(n.TypeName) + "(" + strings.Join(parts, ", ") + ")"
	case *ast.MatchVariantPattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			if arg.Name != "" {
				parts = append(parts, arg.Name+": "+formatMatchPattern(arg.Pattern))
			} else {
				parts = append(parts, formatMatchPattern(arg.Pattern))
			}
		}
		// The enum is reached through a module path (`::`), the variant with `.`.
		line := ast.ModulePathSpelling(n.EnumName) + "." + n.Variant
		if len(parts) != 0 {
			line += "(" + strings.Join(parts, ", ") + ")"
		}
		return line
	default:
		return "<pattern>"
	}
}

// selfParenthesizingExpr reports whether formatExpr already wraps this node in its own
// parentheses. A ParenExpr around one of these must NOT add a second pair: the source
// `(a + b)` parses to ParenExpr{BinaryExpr} and would otherwise print `((a + b))`, so
// each -emit fmt pass grew the output without bound and the formatter was not idempotent.
//
// Keep this in sync with the cases in formatExpr that return a "(" ... ")" string.
func selfParenthesizingExpr(expr ast.Expr) bool {
	switch node := expr.(type) {
	case *ast.BinaryExpr:
		return node != nil
	case *ast.UnaryExpr:
		return node != nil
	case *ast.TernaryExpr:
		return node != nil
	default:
		return false
	}
}
