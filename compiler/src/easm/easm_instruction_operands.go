package easm

import (
	"fmt"
	"strings"
)

func writesPartialReturnRegister(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[len(parts)-1])), "%")
	switch dst {
	case "al", "ah", "ax":
		return true
	default:
		return false
	}
}

func returnsWideInteger(returnType string) bool {
	switch strings.TrimSpace(returnType) {
	case "i64", "u64", "usize", "uintptr":
		return true
	default:
		return false
	}
}

func splitInstructionOperands(text string) []string {
	fields := strings.Fields(text)
	if len(fields) <= 1 {
		return nil
	}
	operandText := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	var out []string
	var current strings.Builder
	depth := 0
	for _, r := range operandText {
		switch r {
		case '(':
			depth++
			current.WriteRune(r)
		case ')':
			if depth > 0 {
				depth--
			}
			current.WriteRune(r)
		case ',':
			if depth == 0 {
				part := strings.TrimSpace(current.String())
				if part != "" {
					out = append(out, part)
				}
				current.Reset()
				continue
			}
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	part := strings.TrimSpace(current.String())
	if part != "" {
		out = append(out, part)
	}
	return out
}

func immediateValue(text string) int {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == '$' || r == '\t'
	})
	for _, field := range fields {
		var value int
		if _, err := fmt.Sscanf(field, "%d", &value); err == nil {
			return value
		}
	}
	return 0
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
