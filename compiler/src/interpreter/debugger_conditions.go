package interpreter

import (
	"fmt"
	"strconv"
	"strings"
)

func evalDebugConditionExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	expr = trimDebugParens(strings.TrimSpace(expr))
	if expr == "" {
		return false, fmt.Errorf("empty expression")
	}
	return evalDebugOrExpr(snapshot, expr)
}

func evalDebugOrExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	parts := splitDebugExpr(expr, " or ")
	if len(parts) > 1 {
		for _, part := range parts {
			value, err := evalDebugAndExpr(snapshot, part)
			if err != nil {
				return false, err
			}
			if value {
				return true, nil
			}
		}
		return false, nil
	}
	return evalDebugAndExpr(snapshot, expr)
}

func evalDebugAndExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	parts := splitDebugExpr(expr, " and ")
	if len(parts) > 1 {
		for _, part := range parts {
			value, err := evalDebugNotExpr(snapshot, part)
			if err != nil {
				return false, err
			}
			if !value {
				return false, nil
			}
		}
		return true, nil
	}
	return evalDebugNotExpr(snapshot, expr)
}

func evalDebugNotExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	expr = trimDebugParens(strings.TrimSpace(expr))
	if strings.HasPrefix(expr, "not ") {
		value, err := evalDebugNotExpr(snapshot, strings.TrimSpace(strings.TrimPrefix(expr, "not ")))
		return !value, err
	}
	return evalDebugCompareExpr(snapshot, expr)
}

func evalDebugCompareExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		left, right, ok := splitDebugComparison(expr, op)
		if !ok {
			continue
		}
		leftValue, err := evalDebugOperandOrArith(snapshot, left)
		if err != nil {
			return false, err
		}
		rightValue, err := evalDebugOperandOrArith(snapshot, right)
		if err != nil {
			return false, err
		}
		return compareDebugOperands(leftValue, rightValue, op)
	}
	value, err := evalDebugOperandOrArith(snapshot, expr)
	if err != nil {
		return false, err
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expression %q did not evaluate to bool", expr)
	}
}

func evalDebugOperand(snapshot DebugSnapshot, operand string) (string, error) {
	operand = trimDebugParens(strings.TrimSpace(operand))
	if operand == "true" || operand == "false" {
		return operand, nil
	}
	if len(operand) >= 2 && operand[0] == '"' && operand[len(operand)-1] == '"' {
		return strings.Trim(operand, "\""), nil
	}
	if value, ok := snapshot.LookupString(operand); ok {
		return value, nil
	}
	if _, ok := parseDebugInt(operand); ok {
		// A decimal or hex integer literal (0xF00, 3840, -5); compareDebugOperands parses it.
		return operand, nil
	}
	if looksLikeDebugPath(operand) {
		// A syntactically valid identifier path that is absent from this snapshot is simply
		// not in scope at this step -- e.g. a local declared later in the function. Signal
		// that distinctly (errDebugOperandNotInScope) so the caller can keep stepping until
		// the variable exists, instead of aborting on the first snapshot. A path that is
		// NEVER in scope across the whole run is reported as an error at completion, so a
		// genuine typo is still caught.
		return "", errDebugOperandNotInScope
	}
	return "", fmt.Errorf("unknown debug operand %q", operand)
}

// errDebugOperandNotInScope marks a break-condition operand that is a valid identifier path
// but is not present in the current snapshot (not yet -- or never -- in scope).
var errDebugOperandNotInScope = fmt.Errorf("debug operand not in scope")

// looksLikeDebugPath reports whether operand is a dotted identifier path such as "foo" or
// "foo.bar" -- a variable/field reference rather than a literal or malformed token.
func looksLikeDebugPath(operand string) bool {
	if operand == "" {
		return false
	}
	for i, r := range operand {
		isAlpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 {
			if !isAlpha {
				return false
			}
			continue
		}
		if !isAlpha && !isDigit && r != '.' {
			return false
		}
	}
	return true
}

// parseDebugInt parses a decimal or hex (0x...) integer literal, including the full u64
// range and an optional leading minus, returning the bit-equivalent int64.
func parseDebugInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if v, err := strconv.ParseInt(s, 0, 64); err == nil {
		return v, true
	}
	if v, err := strconv.ParseUint(s, 0, 64); err == nil {
		return int64(v), true
	}
	return 0, false
}

// evalDebugOperandOrArith evaluates one side of a comparison, supporting integer
// arithmetic over operands -- e.g. "base + 0x1000" or "seg0 - 4" -- where each operand is
// a hex/decimal literal or a variable path that resolves to an integer. Operators must be
// space-separated. When the side is not arithmetic it falls back to plain operand
// resolution (paths, literals, true/false), preserving the not-in-scope sentinel.
func evalDebugOperandOrArith(snapshot DebugSnapshot, side string) (string, error) {
	side = trimDebugParens(strings.TrimSpace(side))
	if idx := strings.Index(side, " + "); idx >= 0 {
		return combineDebugArith(snapshot, side[:idx], side[idx+3:], false)
	}
	if idx := strings.Index(side, " - "); idx >= 0 {
		return combineDebugArith(snapshot, side[:idx], side[idx+3:], true)
	}
	return evalDebugOperand(snapshot, side)
}

func combineDebugArith(snapshot DebugSnapshot, left string, right string, subtract bool) (string, error) {
	leftVal, err := evalDebugOperandOrArith(snapshot, left)
	if err != nil {
		return "", err
	}
	rightVal, err := evalDebugOperandOrArith(snapshot, right)
	if err != nil {
		return "", err
	}
	leftNum, leftOK := parseDebugInt(leftVal)
	rightNum, rightOK := parseDebugInt(rightVal)
	if !leftOK || !rightOK {
		return "", fmt.Errorf("arithmetic requires integer operands, got %q and %q", leftVal, rightVal)
	}
	if subtract {
		return strconv.FormatInt(leftNum-rightNum, 10), nil
	}
	return strconv.FormatInt(leftNum+rightNum, 10), nil
}

func compareDebugOperands(left string, right string, op string) (bool, error) {
	// Integer-aware comparison first: this handles hex literals (0x...), the full u64
	// range, and a numeric value compared against a decimal/hex literal regardless of how
	// the snapshot stringified it.
	if leftInt, leftOK := parseDebugInt(left); leftOK {
		if rightInt, rightOK := parseDebugInt(right); rightOK {
			switch op {
			case "==":
				return leftInt == rightInt, nil
			case "!=":
				return leftInt != rightInt, nil
			case ">":
				return leftInt > rightInt, nil
			case ">=":
				return leftInt >= rightInt, nil
			case "<":
				return leftInt < rightInt, nil
			case "<=":
				return leftInt <= rightInt, nil
			}
		}
	}
	switch op {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	}
	leftNumber, leftErr := strconv.ParseFloat(left, 64)
	rightNumber, rightErr := strconv.ParseFloat(right, 64)
	if leftErr != nil || rightErr != nil {
		return false, fmt.Errorf("operator %s requires numeric operands, got %q and %q", op, left, right)
	}
	switch op {
	case ">":
		return leftNumber > rightNumber, nil
	case ">=":
		return leftNumber >= rightNumber, nil
	case "<":
		return leftNumber < rightNumber, nil
	case "<=":
		return leftNumber <= rightNumber, nil
	default:
		return false, fmt.Errorf("unsupported debug operator %q", op)
	}
}

func trimDebugParens(expr string) string {
	for {
		expr = strings.TrimSpace(expr)
		if len(expr) < 2 || expr[0] != '(' || expr[len(expr)-1] != ')' {
			return expr
		}
		depth := 0
		inString := false
		wraps := true
		for index := 0; index < len(expr); index++ {
			switch expr[index] {
			case '"':
				inString = !inString
			case '(':
				if !inString {
					depth++
				}
			case ')':
				if !inString {
					depth--
					if depth == 0 && index != len(expr)-1 {
						wraps = false
					}
				}
			}
		}
		if !wraps {
			return expr
		}
		expr = expr[1 : len(expr)-1]
	}
}

func splitDebugComparison(expr string, op string) (string, string, bool) {
	parts := splitDebugExpr(expr, op)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func splitDebugExpr(expr string, sep string) []string {
	parts := []string{}
	inString := false
	depth := 0
	start := 0
	for index := 0; index <= len(expr)-len(sep); index++ {
		switch expr[index] {
		case '"':
			inString = !inString
			continue
		case '(':
			if !inString {
				depth++
			}
		case ')':
			if !inString && depth > 0 {
				depth--
			}
		}
		if inString || depth != 0 {
			continue
		}
		if strings.HasPrefix(expr[index:], sep) {
			parts = append(parts, strings.TrimSpace(expr[start:index]))
			start = index + len(sep)
			index = start - 1
		}
	}
	if len(parts) == 0 {
		return []string{strings.TrimSpace(expr)}
	}
	parts = append(parts, strings.TrimSpace(expr[start:]))
	return parts
}
