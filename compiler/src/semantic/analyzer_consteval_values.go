package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

func cloneConstValue(value ConstValue) ConstValue {
	return CloneConstValue(value)
}

func constOptionalSome(value ConstValue) ConstValue {
	cloned := cloneConstValue(value)
	return ConstValue{Kind: ConstOptional, Some: true, Value: &cloned}
}

func constOptionalFromFirstProjection(value ConstValue) ConstValue {
	if value.Kind == ConstOptional {
		return cloneConstValue(value)
	}
	return constOptionalSome(value)
}

func (a *Analyzer) setConstEvalValue(name string, value ConstValue) {
	if len(a.constEvalScopes) == 0 {
		a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{})
	}
	a.constEvalScopes[len(a.constEvalScopes)-1][name] = cloneConstValue(value)
}

func (a *Analyzer) updateConstEvalValue(name string, value ConstValue) bool {
	for i := len(a.constEvalScopes) - 1; i >= 0; i-- {
		if _, ok := a.constEvalScopes[i][name]; ok {
			a.constEvalScopes[i][name] = cloneConstValue(value)
			return true
		}
	}
	return false
}

func (a *Analyzer) constEvalValueScope(name string) (ConstValue, int, bool) {
	for i := len(a.constEvalScopes) - 1; i >= 0; i-- {
		if value, ok := a.constEvalScopes[i][name]; ok {
			return value, i, true
		}
	}
	return ConstValue{}, -1, false
}

func (a *Analyzer) reportStaticAssertFailure(pos lexer.Pos, message ast.Expr) {
	if message != nil {
		if msg, ok := a.evalConstStringExpr(message); ok {
			a.errorf(pos, "static assert failed: %s", msg)
			return
		}
	}
	a.errorf(pos, "static assert failed")
}

func (a *Analyzer) evalConstEquality(left, right ConstValue, equal bool) (ConstValue, bool) {
	matched := false
	switch {
	case left.Kind == ConstInt && right.Kind == ConstInt:
		matched = left.Int == right.Int
	case isConstNumeric(left) && isConstNumeric(right):
		matched = constNumericEqual(left, right)
	case left.Kind == ConstBool && right.Kind == ConstBool:
		matched = left.Bool == right.Bool
	case left.Kind == ConstString && right.Kind == ConstString:
		matched = left.String == right.String
	default:
		return ConstValue{}, false
	}
	if !equal {
		matched = !matched
	}
	return ConstValue{Kind: ConstBool, Bool: matched}, true
}

func evalConstNumericBinary(op lexer.TokenKind, left, right ConstValue) (ConstValue, bool) {
	if left.Kind == ConstInt && right.Kind == ConstInt {
		return evalConstIntBinary(op, left.Int, right.Int)
	}
	if !isConstNumeric(left) || !isConstNumeric(right) {
		return ConstValue{}, false
	}
	leftFloat := constNumericAsFloat64(left)
	rightFloat := constNumericAsFloat64(right)
	switch op {
	case lexer.TOKEN_LT:
		return ConstValue{Kind: ConstBool, Bool: leftFloat < rightFloat}, true
	case lexer.TOKEN_GT:
		return ConstValue{Kind: ConstBool, Bool: leftFloat > rightFloat}, true
	case lexer.TOKEN_LTEQ:
		return ConstValue{Kind: ConstBool, Bool: leftFloat <= rightFloat}, true
	case lexer.TOKEN_GTEQ:
		return ConstValue{Kind: ConstBool, Bool: leftFloat >= rightFloat}, true
	case lexer.TOKEN_PLUS:
		return ConstValue{Kind: ConstFloat, Float: leftFloat + rightFloat}, true
	case lexer.TOKEN_MINUS:
		return ConstValue{Kind: ConstFloat, Float: leftFloat - rightFloat}, true
	case lexer.TOKEN_STAR:
		return ConstValue{Kind: ConstFloat, Float: leftFloat * rightFloat}, true
	case lexer.TOKEN_SLASH:
		if rightFloat == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstFloat, Float: leftFloat / rightFloat}, true
	default:
		return ConstValue{}, false
	}
}

func isConstNumeric(value ConstValue) bool {
	return value.Kind == ConstInt || value.Kind == ConstFloat
}

func constNumericAsFloat64(value ConstValue) float64 {
	if value.Kind == ConstFloat {
		return value.Float
	}
	return float64(value.Int)
}

func constNumericEqual(left, right ConstValue) bool {
	return constNumericAsFloat64(left) == constNumericAsFloat64(right)
}

func evalConstIntBinary(op lexer.TokenKind, left, right int64) (ConstValue, bool) {
	switch op {
	case lexer.TOKEN_LT:
		return ConstValue{Kind: ConstBool, Bool: left < right}, true
	case lexer.TOKEN_GT:
		return ConstValue{Kind: ConstBool, Bool: left > right}, true
	case lexer.TOKEN_LTEQ:
		return ConstValue{Kind: ConstBool, Bool: left <= right}, true
	case lexer.TOKEN_GTEQ:
		return ConstValue{Kind: ConstBool, Bool: left >= right}, true
	case lexer.TOKEN_PLUS:
		return ConstValue{Kind: ConstInt, Int: left + right}, true
	case lexer.TOKEN_MINUS:
		return ConstValue{Kind: ConstInt, Int: left - right}, true
	case lexer.TOKEN_STAR:
		return ConstValue{Kind: ConstInt, Int: left * right}, true
	case lexer.TOKEN_SLASH:
		if right == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: left / right}, true
	case lexer.TOKEN_PERCENT:
		if right == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: left % right}, true
	case lexer.TOKEN_CARET:
		return ConstValue{Kind: ConstInt, Int: left ^ right}, true
	case lexer.TOKEN_PIPE:
		return ConstValue{Kind: ConstInt, Int: left | right}, true
	case lexer.TOKEN_AMPERSAND:
		return ConstValue{Kind: ConstInt, Int: left & right}, true
	case lexer.TOKEN_LSHIFT:
		return ConstValue{Kind: ConstInt, Int: left << right}, true
	case lexer.TOKEN_RSHIFT:
		return ConstValue{Kind: ConstInt, Int: left >> right}, true
	default:
		return ConstValue{}, false
	}
}

func (a *Analyzer) errorf(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	a.appendDiagnostic(Diagnostic{Pos: pos, Severity: DiagnosticSeverityError, Message: fmt.Sprintf(format, formatDiagnosticArgs(args)...)})
}

func (a *Analyzer) warnf(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	a.appendDiagnostic(Diagnostic{Pos: pos, Severity: DiagnosticSeverityWarning, Message: fmt.Sprintf(format, formatDiagnosticArgs(args)...)})
}

// appendDiagnostic records a diagnostic once: a parameter type is resolved when the
// signature is collected AND when the body is analyzed, so an `unknown type` on it used
// to print twice at the same span. The same position, severity and text carry no new
// information the second time.
func (a *Analyzer) appendDiagnostic(diagnostic Diagnostic) {
	key := diagnosticIdentity{Pos: diagnostic.Pos, Severity: diagnostic.Severity, Message: diagnostic.Message}
	if a.reportedDiagnostics == nil {
		a.reportedDiagnostics = map[diagnosticIdentity]bool{}
	}
	if a.reportedDiagnostics[key] {
		return
	}
	a.reportedDiagnostics[key] = true
	a.diagnostics = append(a.diagnostics, diagnostic)
}

type diagnosticIdentity struct {
	Pos      lexer.Pos
	Severity DiagnosticSeverity
	Message  string
}

// warnOncef emits a warning at most once per position+message, for checks that
// run on paths visited more than once per declaration (e.g. funcTypeFromDecl).
func (a *Analyzer) warnOncef(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	message := fmt.Sprintf(format, formatDiagnosticArgs(args)...)
	key := fmt.Sprintf("%s:%d:%d:%s", pos.File, pos.Line, pos.Col, message)
	if a.warnOnceSeen == nil {
		a.warnOnceSeen = make(map[string]bool)
	}
	if a.warnOnceSeen[key] {
		return
	}
	a.warnOnceSeen[key] = true
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityWarning, Message: message})
}
