package unparse

import (
	"llcontext/src/ast"
	"strconv"
	"strings"
	"unicode/utf8"
)

func formatMoveBindPattern(pattern ast.MoveBindPattern) string {
	switch n := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return n.Name
	case *ast.MoveBindStructPattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			if n.Brace {
				part := arg.Field
				if part == "" {
					part = arg.Name
				}
				if arg.Name != "" && arg.Name != part {
					part += ": " + arg.Name
				}
				parts = append(parts, part)
				continue
			}
			parts = append(parts, arg.Name)
		}
		if n.Brace {
			if n.TypeName == "" {
				return "{" + strings.Join(parts, ", ") + "}"
			}
			return n.TypeName + "{" + strings.Join(parts, ", ") + "}"
		}
		return n.TypeName + "(" + strings.Join(parts, ", ") + ")"
	case *ast.MoveBindTuplePattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, arg.Name)
		}
		return strings.Join(parts, ", ")
	case *ast.MoveBindVariantPattern:
		return formatMoveBindVariantPattern(n)
	default:
		return "<move-pattern>"
	}
}
func formatMoveBindVariantPattern(pattern *ast.MoveBindVariantPattern) string {
	if pattern == nil {
		return "<move-variant-pattern>"
	}
	parts := make([]string, 0, len(pattern.Args))
	for _, arg := range pattern.Args {
		if arg.Name != "" {
			parts = append(parts, arg.Name+": "+formatMatchPattern(arg.Pattern))
		} else {
			parts = append(parts, formatMatchPattern(arg.Pattern))
		}
	}
	line := pattern.EnumName + "." + pattern.Variant
	if len(parts) != 0 {
		line += "(" + strings.Join(parts, ", ") + ")"
	}
	return line
}
func indentMultiline(text string, level int) string {
	if text == "" {
		return ""
	}
	indent := strings.Repeat(indentUnit, level)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n") + "\n"
}
func formatCharLiteral(value string) string {
	if value == "" {
		return "'\\0'"
	}
	if utf8.ValidString(value) {
		r, size := utf8.DecodeRuneInString(value)
		if r != utf8.RuneError && size == len(value) {
			return strconv.QuoteRuneToASCII(r)
		}
	}
	if len(value) == 1 {
		return "'" + escapeSingleQuotedByte(value[0]) + "'"
	}
	return "'\\x00'"
}
func escapeSingleQuotedByte(b byte) string {
	switch b {
	case '\\':
		return "\\\\"
	case '\'':
		return "\\'"
	case '\n':
		return "\\n"
	case '\r':
		return "\\r"
	case '\t':
		return "\\t"
	case 0:
		return "\\0"
	default:
		if b < 0x20 || b >= 0x7f {
			hex := strings.ToUpper(strconv.FormatInt(int64(b), 16))
			if len(hex) < 2 {
				hex = "0" + hex
			}
			return "\\x" + hex
		}
		return string([]byte{b})
	}
}
