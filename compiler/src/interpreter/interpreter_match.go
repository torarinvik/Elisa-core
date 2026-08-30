package interpreter

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

// execMatchStmt executes the lowered statement form used by both source matches and
// machine dispatch. Machine lowering deliberately leaves a MatchStmt in the AST: the
// coverage marker is compile-time-only, while this dispatch is the runtime operation.
func (i *Interpreter) execMatchStmt(frame *frame, stmt *ast.MatchStmt) (controlSignal, error) {
	value, err := i.evalExpr(frame, stmt.Value)
	if err != nil {
		return controlSignal{}, annotateRuntimeError(stmt.Pos(), err)
	}
	for _, arm := range stmt.Arms {
		bindings := map[string]Value{}
		matched, err := i.matchPattern(value, arm.Pattern, bindings)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(arm.Position, err)
		}
		if !matched {
			continue
		}
		armFrame := childFrame(frame)
		for name, bound := range bindings {
			armFrame.locals[name] = bound.Clone()
		}
		if arm.Guard != nil {
			guard, err := i.evalExpr(armFrame, arm.Guard)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(arm.Position, err)
			}
			truth, err := requireBool(guard)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(arm.Position, err)
			}
			if !truth {
				continue
			}
		}
		if stmt.Store != nil {
			slot, err := i.resolveSlot(frame, stmt.Store)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(stmt.Pos(), err)
			}
			if err := slot.set(value); err != nil {
				return controlSignal{}, annotateRuntimeError(stmt.Pos(), err)
			}
		}
		return i.execBlock(armFrame, arm.Body)
	}
	return controlSignal{}, annotateRuntimeError(stmt.Pos(), fmt.Errorf("non-exhaustive match at runtime"))
}

func (i *Interpreter) matchPattern(value Value, pattern ast.MatchPattern, bindings map[string]Value) (bool, error) {
	if pattern == nil {
		return false, fmt.Errorf("nil match pattern")
	}
	value = derefValue(value)
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return true, nil
	case *ast.MatchBindPattern:
		if p.Name != "" {
			bindings[p.Name] = value.Clone()
		}
		return true, nil
	case *ast.MatchStringLiteralPattern:
		return value.kind == valueString && value.strVal == p.Value, nil
	case *ast.MatchLiteralPattern:
		literal, err := i.evalExpr(nil, p.Value)
		if err != nil {
			return false, err
		}
		return valuesEqual(value, literal), nil
	case *ast.MatchVariantPattern:
		actual, err := requireInt(value)
		if err != nil {
			return false, err
		}
		if enumType, ok := i.result.NamedTypes[p.EnumName].(*semantic.EnumType); ok && enumType != nil {
			variant, ok := enumType.Variant(p.Variant)
			if !ok {
				return false, fmt.Errorf("unknown enum variant %s.%s", p.EnumName, p.Variant)
			}
			return actual == int64(variant.Tag), nil
		}
		if constEnumType, ok := i.result.NamedTypes[p.EnumName].(*semantic.ConstEnumType); ok && constEnumType != nil {
			member, ok := constEnumType.Member(p.Variant)
			if !ok {
				return false, fmt.Errorf("unknown const enum member %s.%s", p.EnumName, p.Variant)
			}
			return actual == member.Value, nil
		}
		return false, fmt.Errorf("unknown match enum %q", p.EnumName)
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			optionBindings := map[string]Value{}
			matched, err := i.matchPattern(value, option, optionBindings)
			if err != nil {
				return false, err
			}
			if matched {
				for name, bound := range optionBindings {
					bindings[name] = bound
				}
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("unsupported interpreter match pattern %T", pattern)
	}
}
