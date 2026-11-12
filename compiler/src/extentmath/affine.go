package extentmath

import (
	"fmt"
	"sort"
	"strings"
)

type Expr struct {
	Const int64
	Terms map[string]int64
}

func newExpr() Expr {
	return Expr{Terms: map[string]int64{}}
}
func (e *Expr) add(other Expr) {
	if e == nil {
		return
	}
	e.Const += other.Const
	for term, coeff := range other.Terms {
		e.addTerm(term, coeff)
	}
}
func (e *Expr) addTerm(term string, coeff int64) {
	if e == nil || term == "" || coeff == 0 {
		return
	}
	if e.Terms == nil {
		e.Terms = map[string]int64{}
	}
	e.Terms[term] += coeff
	if e.Terms[term] == 0 {
		delete(e.Terms, term)
	}
}
func (e Expr) scaled(factor int64) Expr {
	out := newExpr()
	out.Const = e.Const * factor
	for term, coeff := range e.Terms {
		out.Terms[term] = coeff * factor
	}
	return out
}
func (e Expr) Sub(other Expr) Expr {
	out := newExpr()
	out.Const = e.Const - other.Const
	for term, coeff := range e.Terms {
		out.Terms[term] = coeff
	}
	for term, coeff := range other.Terms {
		out.addTerm(term, -coeff)
	}
	return out
}
func IsConst(expr Expr) bool {
	return len(expr.Terms) == 0
}
func TermsEqual(left Expr, right Expr) bool {
	if len(left.Terms) != len(right.Terms) {
		return false
	}
	for term, coeff := range left.Terms {
		if right.Terms[term] != coeff {
			return false
		}
	}
	return true
}
func Equal(left Expr, right Expr) bool {
	return left.Const == right.Const && TermsEqual(left, right)
}
func Compare(left Expr, right Expr) (int, bool) {
	if !TermsEqual(left, right) {
		return 0, false
	}
	switch {
	case left.Const < right.Const:
		return -1, true
	case left.Const > right.Const:
		return 1, true
	default:
		return 0, true
	}
}
func CompareByPositiveSymbol(left Expr, right Expr, symbol string) (int, bool) {
	if symbol == "" {
		return 0, false
	}
	leftResidual := newExpr()
	leftResidual.Const = left.Const
	for term, coeff := range left.Terms {
		if term == symbol {
			continue
		}
		leftResidual.Terms[term] = coeff
	}
	rightResidual := newExpr()
	rightResidual.Const = right.Const
	for term, coeff := range right.Terms {
		if term == symbol {
			continue
		}
		rightResidual.Terms[term] = coeff
	}
	if !Equal(leftResidual, rightResidual) {
		return 0, false
	}
	leftCoeff := left.Terms[symbol]
	rightCoeff := right.Terms[symbol]
	switch {
	case leftCoeff < rightCoeff:
		return -1, true
	case leftCoeff > rightCoeff:
		return 1, true
	default:
		return 0, true
	}
}
func CompareBySinglePositiveTerm(left Expr, right Expr) (int, bool) {
	leftResidual := newExpr()
	rightResidual := newExpr()
	leftResidual.Const = left.Const
	rightResidual.Const = right.Const
	varyingTerm := ""
	seen := map[string]bool{}
	for term := range left.Terms {
		seen[term] = true
	}
	for term := range right.Terms {
		seen[term] = true
	}
	for term := range seen {
		leftCoeff := left.Terms[term]
		rightCoeff := right.Terms[term]
		if leftCoeff == rightCoeff {
			if leftCoeff != 0 {
				leftResidual.Terms[term] = leftCoeff
				rightResidual.Terms[term] = rightCoeff
			}
			continue
		}
		if varyingTerm != "" {
			return 0, false
		}
		varyingTerm = term
	}
	if varyingTerm == "" {
		return 0, false
	}
	if !Equal(leftResidual, rightResidual) {
		return 0, false
	}
	return CompareByPositiveSymbol(left, right, varyingTerm)
}
func String(expr Expr) string {
	parts := make([]string, 0, len(expr.Terms)+1)
	keys := make([]string, 0, len(expr.Terms))
	for term := range expr.Terms {
		keys = append(keys, term)
	}
	sort.Strings(keys)
	for _, term := range keys {
		coeff := expr.Terms[term]
		switch coeff {
		case 1:
			parts = append(parts, term)
		case -1:
			parts = append(parts, fmt.Sprintf("(-1 * %s)", term))
		default:
			parts = append(parts, fmt.Sprintf("(%d * %s)", coeff, term))
		}
	}
	if expr.Const != 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d", expr.Const))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, " + ")
}
func Parse(value string) (Expr, bool) {
	trimmed := Trim(value)
	if trimmed == "" {
		return Expr{}, false
	}
	if parts := SplitTopLevel(trimmed, " + "); len(parts) > 1 {
		out := newExpr()
		for _, part := range parts {
			parsed, ok := Parse(part)
			if !ok {
				return Expr{}, false
			}
			out.add(parsed)
		}
		return out, true
	}
	if parts := SplitTopLevel(trimmed, " * "); len(parts) == 2 {
		left, leftOK := Parse(parts[0])
		right, rightOK := Parse(parts[1])
		if !leftOK || !rightOK {
			return Expr{}, false
		}
		switch {
		case IsConst(left):
			return right.scaled(left.Const), true
		case IsConst(right):
			return left.scaled(right.Const), true
		default:
			return Expr{}, false
		}
	}
	if constValue, ok := constInt(trimmed); ok {
		out := newExpr()
		out.Const = constValue
		return out, true
	}
	out := newExpr()
	out.addTerm(trimmed, 1)
	return out, true
}
func Trim(value string) string {
	trimmed := value
	for len(trimmed) >= 2 && trimmed[0] == '(' && trimmed[len(trimmed)-1] == ')' {
		depth := 0
		balanced := true
		for i := 0; i < len(trimmed); i++ {
			switch trimmed[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth < 0 {
					balanced = false
				}
				if depth == 0 && i != len(trimmed)-1 {
					balanced = false
				}
			}
		}
		if !balanced || depth != 0 {
			break
		}
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}
func SplitTopLevel(value string, sep string) []string {
	if value == "" || sep == "" || !strings.Contains(value, sep) {
		return nil
	}
	parts := make([]string, 0, 2)
	depth := 0
	start := 0
	found := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && i+len(sep) <= len(value) && value[i:i+len(sep)] == sep {
			parts = append(parts, value[start:i])
			start = i + len(sep)
			i += len(sep) - 1
			found = true
		}
	}
	if !found {
		return nil
	}
	parts = append(parts, value[start:])
	return parts
}
func constInt(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	trimmed := value
	for len(trimmed) >= 2 && trimmed[0] == '(' && trimmed[len(trimmed)-1] == ')' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	if trimmed == "" {
		return 0, false
	}
	if last := trimmed[len(trimmed)-1]; last == 'u' || last == 'i' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return 0, false
	}
	value64, err := parseInt(trimmed)
	if err != nil {
		return 0, false
	}
	return value64, true
}
func parseInt(value string) (int64, error) {
	negative := false
	if value != "" && value[0] == '-' {
		negative = true
		value = value[1:]
	}
	if value == "" {
		return 0, fmt.Errorf("empty int")
	}
	var out int64
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-decimal int %q", value)
		}
		out = out*10 + int64(ch-'0')
	}
	if negative {
		out = -out
	}
	return out, nil
}
