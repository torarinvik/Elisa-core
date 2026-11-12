package vcarithmetic

import "math"

// FoldArith evaluates a constant machine op in int64 with overflow detection; on overflow it declines
// (keeps the term structural) rather than fold a wrong value.
func FoldArith(op string, a, b int64) (int64, bool) {
	switch op {
	case "+":
		s := a + b
		if (s > a) != (b > 0) {
			return 0, false
		}
		return s, true
	case "-":
		d := a - b
		if (d < a) != (b > 0) {
			return 0, false
		}
		return d, true
	case "*":
		if a == math.MinInt64 && b == -1 || b == math.MinInt64 && a == -1 {
			return 0, false
		}
		if a == 0 || b == 0 {
			return 0, true
		}
		p := a * b
		if p/b != a {
			return 0, false
		}
		return p, true
	}
	return 0, false
}
