package interpreter

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (i *Interpreter) evalIsExpr(frame *frame, expr *ast.BinaryExpr) (Value, error) {
	if expr == nil {
		return VoidValue(), fmt.Errorf("is expression is nil")
	}
	leftValue, err := i.evalExpr(frame, expr.Left)
	if err != nil {
		return VoidValue(), err
	}
	for _, target := range flattenInterpreterIsTargets(expr.Right) {
		base, cases, ok := i.namedStateIsTarget(target)
		if !ok || base == nil {
			return VoidValue(), fmt.Errorf("interpreter only supports named-state struct tests for is")
		}
		if leftValue.kind != valueStruct || leftValue.structVal == nil {
			return VoidValue(), fmt.Errorf("is requires a struct value, got %s", leftValue.String())
		}
		if leftValue.structVal.Name != base.Name {
			continue
		}
		if len(cases) == len(base.NamedStateCases) {
			return BoolValue(true), nil
		}
		for _, stateName := range cases {
			derived := base.DerivedStateMap[stateName]
			if derived == nil || derived.Condition == nil {
				return VoidValue(), fmt.Errorf("missing derived state rule for %s.%s", base.Name, stateName)
			}
			value, err := i.evalDerivedStateExpr(frame, derived.Condition, leftValue)
			if err != nil {
				return VoidValue(), err
			}
			truth, err := requireBool(value)
			if err != nil {
				return VoidValue(), err
			}
			if truth {
				return BoolValue(true), nil
			}
		}
	}
	return BoolValue(false), nil
}
func (i *Interpreter) evalMembershipExpr(frame *frame, expr *ast.BinaryExpr) (Value, error) {
	if expr == nil {
		return VoidValue(), fmt.Errorf("membership expression is nil")
	}
	list, ok := expr.Right.(*ast.ListLitExpr)
	if !ok || list == nil {
		return VoidValue(), fmt.Errorf("membership operator requires a list literal on the right-hand side")
	}
	leftValue, err := i.evalExpr(frame, expr.Left)
	if err != nil {
		return VoidValue(), err
	}
	for _, elem := range list.Elems {
		if rangeExpr, ok := elem.(*ast.MembershipRangeExpr); ok {
			matched, err := i.evalMembershipRangeExpr(frame, leftValue, rangeExpr)
			if err != nil {
				return VoidValue(), err
			}
			if matched {
				return BoolValue(true), nil
			}
			continue
		}
		candidate, err := i.evalExpr(frame, elem)
		if err != nil {
			return VoidValue(), err
		}
		if valuesEqual(leftValue, candidate) {
			return BoolValue(true), nil
		}
	}
	return BoolValue(false), nil
}

func (i *Interpreter) evalMembershipRangeExpr(frame *frame, value Value, expr *ast.MembershipRangeExpr) (bool, error) {
	if expr == nil {
		return false, fmt.Errorf("membership range expression is nil")
	}
	start, err := i.evalExpr(frame, expr.Start)
	if err != nil {
		return false, err
	}
	end, err := i.evalExpr(frame, expr.End)
	if err != nil {
		return false, err
	}
	if value.kind != valueInt || start.kind != valueInt || end.kind != valueInt {
		return false, fmt.Errorf("membership ranges require integer-compatible values")
	}
	if value.int64Val < start.int64Val {
		return false, nil
	}
	if expr.Op == lexer.TOKEN_RANGE_LT {
		return value.int64Val < end.int64Val, nil
	}
	return value.int64Val <= end.int64Val, nil
}

func flattenInterpreterIsTargets(expr ast.Expr) []ast.Expr {
	if expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return flattenInterpreterIsTargets(n.Inner)
	case *ast.IsPatternExpr:
		out := make([]ast.Expr, 0, len(n.Targets))
		for _, target := range n.Targets {
			out = append(out, flattenInterpreterIsTargets(target)...)
		}
		return out
	default:
		return []ast.Expr{expr}
	}
}
func (i *Interpreter) namedStateIsTarget(expr ast.Expr) (*semantic.StructType, []string, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return i.namedStateIsTarget(paren.Inner)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil || i == nil || i.result == nil {
		return nil, nil, false
	}
	switch n := typedExpr.Type.(type) {
	case *ast.NamedType:
		base, ok := i.result.NamedTypes[n.Name]
		if !ok {
			return nil, nil, false
		}
		structType, ok := base.(*semantic.StructType)
		if !ok || structType == nil || len(structType.NamedStateCases) == 0 {
			return nil, nil, false
		}
		return structType, append([]string(nil), structType.NamedStateCases...), true
	case *ast.GenericType:
		base, ok := i.result.NamedTypes[n.Name]
		if !ok {
			return nil, nil, false
		}
		structType, ok := base.(*semantic.StructType)
		if !ok || structType == nil || len(structType.NamedStateCases) == 0 {
			return nil, nil, false
		}
		if len(n.Args) == 0 {
			return structType, append([]string(nil), structType.NamedStateCases...), true
		}
		switch arg := n.Args[len(n.Args)-1].(type) {
		case *ast.NamedType:
			return structType, []string{arg.Name}, true
		case *ast.StateSetTypeExpr:
			return structType, append([]string(nil), arg.Cases...), true
		default:
			return nil, nil, false
		}
	default:
		return nil, nil, false
	}
}
