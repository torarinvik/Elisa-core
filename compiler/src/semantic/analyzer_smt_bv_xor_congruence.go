package semantic

import (
	"strconv"
	"strings"
)

// Replace a generated unsigned XOR read-back by an uninterpreted Int function
// of its two Int inputs. The real operation is one interpretation of this
// function, so the abstraction remains a weakening while retaining congruence.
// Unsupported shapes keep the existing independent-constant abstraction.
func smtAbstractXorRead(term string) (application, declaration string, ok bool) {
	const prefix = "(bv2nat (bvxor "
	if !strings.HasPrefix(term, prefix) || !strings.HasSuffix(term, "))") {
		return "", "", false
	}
	operands := strings.TrimSpace(term[len(prefix) : len(term)-2])
	end, valid := smtBalancedEnd(operands, 0)
	if !valid || len(operands) == 0 || operands[0] != '(' {
		return "", "", false
	}
	left, right := operands[:end], strings.TrimSpace(operands[end:])
	end, valid = smtBalancedEnd(right, 0)
	if !valid || len(right) == 0 || right[0] != '(' || end != len(right) {
		return "", "", false
	}
	lw, la, lok := smtAbstractInt2BVOperand(left)
	rw, ra, rok := smtAbstractInt2BVOperand(right)
	if !lok || !rok || lw != rw {
		return "", "", false
	}
	name := smtBVAbstractPrefix + "xor$" + strconv.Itoa(lw)
	return "(" + name + " " + la + " " + ra + ")", "(declare-fun " + name + " (Int Int) Int)\n", true
}

func smtAbstractInt2BVOperand(term string) (int, string, bool) {
	const prefix = "((_ int2bv "
	if !strings.HasPrefix(term, prefix) || !strings.HasSuffix(term, ")") {
		return 0, "", false
	}
	close := strings.IndexByte(term[len(prefix):], ')')
	if close < 0 {
		return 0, "", false
	}
	close += len(prefix)
	width, err := strconv.Atoi(term[len(prefix):close])
	if err != nil || width < 1 || width > 64 {
		return 0, "", false
	}
	arg := strings.TrimSpace(term[close+1 : len(term)-1])
	if !smtAbstractSingleTerm(arg) || strings.Contains(arg, "int2bv") || strings.Contains(arg, "bv2nat") {
		return 0, "", false
	}
	return width, arg, true
}

func smtAbstractSingleTerm(term string) bool {
	if term == "" {
		return false
	}
	if term[0] == '(' {
		end, ok := smtBalancedEnd(term, 0)
		return ok && end == len(term)
	}
	if term[0] == '|' {
		return len(term) > 2 && term[len(term)-1] == '|' && !strings.Contains(term[1:len(term)-1], "|")
	}
	return !strings.ContainsAny(term, "() \t\r\n;\"|")
}
