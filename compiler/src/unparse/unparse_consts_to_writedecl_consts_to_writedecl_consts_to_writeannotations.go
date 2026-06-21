package unparse

import (
	"elisacore/src/ast"
	"strings"
)

const indentUnit = "    "

func FormatFile(file *ast.File) string {
	if file == nil {
		return ""
	}
	var f formatter
	for i, decl := range file.Decls {
		if i > 0 {
			f.blankLine()
		}
		f.writeDecl(0, decl)
	}
	return strings.TrimRight(f.builder.String(), "\n") + "\n"
}
func FormatDecl(decl ast.Decl) string {
	if decl == nil {
		return ""
	}
	var f formatter
	f.writeDecl(0, decl)
	return strings.TrimRight(f.builder.String(), "\n")
}
func FormatStmt(stmt ast.Stmt) string {
	if stmt == nil {
		return ""
	}
	var f formatter
	f.writeStmt(0, stmt)
	return strings.TrimRight(f.builder.String(), "\n")
}
func FormatType(typ ast.TypeExpr) string {
	return formatTypeExpr(typ)
}
func FormatExpr(expr ast.Expr) string {
	return formatExpr(expr)
}
func formatPostfixShorthandCastTarget(typ ast.TypeExpr) (string, bool) {
	switch n := typ.(type) {
	case *ast.OptionalTypeExpr:
		if target, ok := formatPostfixShorthandCastTarget(n.Value); ok {
			return target + "?", true
		}
	case *ast.NamedType:
		switch n.Name {
		case "void", "bool", "char", "int",
			"i8", "i16", "i32", "i64", "isize",
			"s8", "s16", "s32", "s64",
			"u8", "u16", "u32", "u64", "usize", "uintptr",
			"f32", "f64":
			return n.Name, true
		}
		if n.Name != "" {
			first := n.Name[0]
			if first >= 'A' && first <= 'Z' {
				return n.Name, true
			}
		}
	}
	return "", false
}

type formatter struct {
	builder strings.Builder
}

func (f *formatter) blankLine() {
	f.builder.WriteByte('\n')
}
func (f *formatter) writeLine(level int, text string) {
	f.builder.WriteString(strings.Repeat(indentUnit, level))
	f.builder.WriteString(text)
	f.builder.WriteByte('\n')
}
func (f *formatter) writePrefixedMultiline(level int, prefix string, text string) {
	if text == "" {
		f.writeLine(level, prefix)
		return
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			continue
		}
		if i == 0 {
			f.writeLine(level, prefix+line)
			continue
		}
		f.writeLine(level, line)
	}
}
func indentMultilineText(text string, prefix string) string {
	if text == "" {
		return prefix
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			lines = lines[:len(lines)-1]
			break
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
func formatLexerCharClassTerm(term ast.LexerCharClassTerm) string {
	if term.Ref {
		return term.Name
	}
	start := formatCharLiteral(term.Start)
	if term.Range {
		return start + ".." + formatCharLiteral(term.End)
	}
	return start
}
func formatLambdaExpr(expr *ast.LambdaExpr) string {
	if expr == nil {
		return "lambda"
	}
	keyword := expr.Keyword
	if keyword == "" {
		keyword = "lambda"
	}
	params := make([]string, 0, len(expr.Params))
	if expr.UsesShorthandParams {
		for _, param := range expr.Params {
			params = append(params, param.Name)
		}
	} else {
		for _, param := range expr.Params {
			part := ""
			if param.Mutable {
				part += "mutable "
			}
			part += param.Name
			// A parenthesized lambda param may omit its type (`lambda(n) => ...`),
			// inferring it from the contextual callback signature.
			if param.Type != nil {
				part += ": " + formatTypeExpr(param.Type)
			}
			params = append(params, part)
		}
	}
	line := keyword + " "
	if expr.UsesShorthandParams {
		line += strings.Join(params, ", ")
	} else {
		line += "(" + strings.Join(params, ", ") + ")"
	}
	if expr.ReturnType != nil {
		line += " -> " + formatTypeExpr(expr.ReturnType)
	}
	if expr.BodyExpr != nil {
		return line + ": " + formatExpr(expr.BodyExpr)
	}
	lines := []string{line + ":"}
	for _, stmt := range expr.Body {
		lines = append(lines, indentMultilineText(formatStmtText(stmt), "    "))
	}
	return strings.Join(lines, "\n")
}
func formatStmtText(stmt ast.Stmt) string {
	var f formatter
	f.writeStmt(0, stmt)
	return strings.TrimSuffix(f.builder.String(), "\n")
}
func (f *formatter) writeAnnotations(level int, annotations []ast.Annotation) {
	for _, annotation := range annotations {
		f.writeLine(level, formatAnnotation(annotation))
	}
}
