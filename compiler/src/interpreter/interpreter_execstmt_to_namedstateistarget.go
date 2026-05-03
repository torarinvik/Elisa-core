package interpreter

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
	"strconv"
	"unicode/utf8"
)

func (i *Interpreter) execStmt(frame *frame, stmt ast.Stmt) (controlSignal, error) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		value, err := i.evaluateInitializer(frame, n.Type, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		frame.locals[n.Name] = value
		return controlSignal{}, nil
	case *ast.AssignStmt:
		if n.Optional {
			if err := i.execOptionalAssignStmt(frame, n); err != nil {
				return controlSignal{}, annotateRuntimeError(n.Pos(), err)
			}
			return controlSignal{}, nil
		}
		value, err := i.evalExpr(frame, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		slot, err := i.resolveSlot(frame, n.Target)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		if err := slot.set(value); err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, nil
	case *ast.AugAssignStmt:
		slot, err := i.resolveSlot(frame, n.Target)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		current := slot.get()
		rhs, err := i.evalExpr(frame, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		updated, err := evalBinaryOp(augAssignBaseOp(n.Op), current, rhs)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		if err := slot.set(updated); err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, nil
	case *ast.LocalParamsStmt:
		return controlSignal{}, nil
	case *ast.ReturnStmt:
		if n.Value == nil {
			return controlSignal{kind: signalReturn, value: VoidValue()}, nil
		}
		value, err := i.evalExpr(frame, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{kind: signalReturn, value: value}, nil
	case *ast.IfStmt:
		cond, err := i.evalExpr(frame, n.Cond)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		if truth, err := requireBool(cond); err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		} else if truth {
			return i.execBlock(frame, n.Then)
		}
		for _, clause := range n.Elifs {
			clauseValue, err := i.evalExpr(frame, clause.Cond)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(clause.Position, err)
			}
			truth, err := requireBool(clauseValue)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(clause.Position, err)
			}
			if truth {
				return i.execBlock(frame, clause.Body)
			}
		}
		return i.execBlock(frame, n.Else)
	case *ast.WhileStmt:
		for iteration := 0; iteration < maxLoopIterations; iteration++ {
			cond, err := i.evalExpr(frame, n.Cond)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(n.Pos(), err)
			}
			truth, err := requireBool(cond)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(n.Pos(), err)
			}
			if !truth {
				return controlSignal{}, nil
			}
			signal, err := i.execBlock(frame, n.Body)
			if err != nil {
				return controlSignal{}, err
			}
			if signal.kind != signalNone {
				return signal, nil
			}
		}
		return controlSignal{}, annotateRuntimeError(n.Pos(), fmt.Errorf("loop iteration limit exceeded (%d)", maxLoopIterations))
	case *ast.PassStmt:
		return controlSignal{}, nil
	case *ast.SignalStmt:
		return controlSignal{}, nil
	case *ast.PanicStmt:
		message, err := i.evalExpr(frame, n.Message)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		text, err := stringifyValue(message)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, fmt.Errorf("panic at %s: %s", n.Pos(), text)
	case *ast.ExprStmt:
		_, err := i.evalExpr(frame, n.Expr)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, nil
	case *ast.DiscardStmt:
		_, err := i.evalExpr(frame, n.Value)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		return controlSignal{}, nil
	case *ast.CanStmt:
		return i.execBlock(frame, n.Body)
	case *ast.WithStmt:
		return i.execBlock(frame, n.Body)
	case *ast.StaticIfStmt:
		cond, err := i.evalExpr(frame, n.Cond)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		truth, err := requireBool(cond)
		if err != nil {
			return controlSignal{}, annotateRuntimeError(n.Pos(), err)
		}
		if truth {
			return i.execBlock(frame, n.Then)
		}
		for _, clause := range n.Elifs {
			value, err := i.evalExpr(frame, clause.Cond)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(clause.Position, err)
			}
			truth, err := requireBool(value)
			if err != nil {
				return controlSignal{}, annotateRuntimeError(clause.Position, err)
			}
			if truth {
				return i.execBlock(frame, clause.Body)
			}
		}
		return i.execBlock(frame, n.Else)
	default:
		return controlSignal{}, annotateRuntimeError(stmt.Pos(), fmt.Errorf("unsupported interpreter statement %T", stmt))
	}
}
func (i *Interpreter) evaluateInitializer(frame *frame, typ ast.TypeExpr, expr ast.Expr) (Value, error) {
	if expr == nil {
		if typ == nil {
			return VoidValue(), nil
		}
		return i.zeroValueForType(typ)
	}
	if _, ok := expr.(*ast.ZeroedLit); ok {
		if typ == nil {
			return VoidValue(), fmt.Errorf("zeroed literal requires an explicit type")
		}
		return i.zeroValueForType(typ)
	}
	return i.evalExpr(frame, expr)
}
func (i *Interpreter) evalExpr(frame *frame, expr ast.Expr) (Value, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		return i.lookupValue(frame, n.Name)
	case *ast.IntLit:
		return parseIntLiteral(n)
	case *ast.FloatLit:
		value, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return VoidValue(), err
		}
		return FloatValue(value), nil
	case *ast.StringLit:
		return StringValue(n.Value), nil
	case *ast.CharLit:
		if n.Value == "" {
			return IntValue(0), nil
		}
		r, _ := utf8.DecodeRuneInString(n.Value)
		return IntValue(int64(r)), nil
	case *ast.BoolLit:
		return BoolValue(n.Value), nil
	case *ast.NullLit:
		return NullValue(), nil
	case *ast.ZeroedLit:
		return VoidValue(), fmt.Errorf("zeroed literal requires typed context")
	case *ast.BinaryExpr:
		if n.LoweredCall != nil {
			return i.evalExpr(frame, n.LoweredCall)
		}
		if n.Op == lexer.TOKEN_IS {
			return i.evalIsExpr(frame, n)
		}
		if n.Op == lexer.TOKEN_IN {
			return i.evalMembershipExpr(frame, n)
		}
		left, err := i.evalExpr(frame, n.Left)
		if err != nil {
			return VoidValue(), err
		}
		right, err := i.evalExpr(frame, n.Right)
		if err != nil {
			return VoidValue(), err
		}
		return evalBinaryOp(n.Op, left, right)
	case *ast.UnaryExpr:
		operand, err := i.evalExpr(frame, n.Operand)
		if err != nil {
			return VoidValue(), err
		}
		return evalUnaryOp(n.Op, operand)
	case *ast.CallExpr:
		return i.evalCallExpr(frame, n)
	case *ast.FieldExpr:
		if n.Safe {
			obj, err := i.evalExpr(frame, n.Object)
			if err != nil {
				return VoidValue(), err
			}
			if obj.IsNull() {
				return NullValue(), nil
			}
			value, err := interpreterFieldValue(obj, n.Field)
			if err != nil {
				return VoidValue(), err
			}
			return value.Clone(), nil
		}
		obj, err := i.evalExpr(frame, n.Object)
		if err != nil {
			return VoidValue(), err
		}
		if obj.kind != valueStruct || obj.structVal == nil {
			return VoidValue(), fmt.Errorf("field access requires a struct, got %s", obj.String())
		}
		value, ok := obj.structVal.Fields[n.Field]
		if !ok {
			return VoidValue(), fmt.Errorf("struct %s has no field %q", obj.structVal.Name, n.Field)
		}
		return value.Clone(), nil
	case *ast.IndexExpr:
		obj, err := i.evalExpr(frame, n.Object)
		if err != nil {
			return VoidValue(), err
		}
		index, err := i.evalExpr(frame, n.Index)
		if err != nil {
			return VoidValue(), err
		}
		idx, err := requireInt(index)
		if err != nil {
			return VoidValue(), err
		}
		if n.Fallback != nil {
			inRange, err := indexInRange(obj, idx)
			if err != nil {
				return VoidValue(), err
			}
			if !inRange {
				return i.evalExpr(frame, n.Fallback)
			}
		}
		return indexValue(obj, idx)
	case *ast.SliceExpr:
		obj, err := i.evalExpr(frame, n.Object)
		if err != nil {
			return VoidValue(), err
		}
		start, err := i.evalExpr(frame, n.Start)
		if err != nil {
			return VoidValue(), err
		}
		end, err := i.evalExpr(frame, n.End)
		if err != nil {
			return VoidValue(), err
		}
		startIndex, err := requireInt(start)
		if err != nil {
			return VoidValue(), err
		}
		endIndex, err := requireInt(end)
		if err != nil {
			return VoidValue(), err
		}
		return sliceValue(obj, startIndex, endIndex)
	case *ast.ListLitExpr:
		values := make([]Value, 0, len(n.Elems))
		for _, elem := range n.Elems {
			value, err := i.evalExpr(frame, elem)
			if err != nil {
				return VoidValue(), err
			}
			values = append(values, value)
		}
		return ListValue(values), nil
	case *ast.CastExpr:
		if _, ok := n.Operand.(*ast.ZeroedLit); ok {
			return i.zeroValueForType(n.Target)
		}
		value, err := i.evalExpr(frame, n.Operand)
		if err != nil {
			return VoidValue(), err
		}
		return i.castValue(value, n.Target)
	case *ast.TernaryExpr:
		cond, err := i.evalExpr(frame, n.Cond)
		if err != nil {
			return VoidValue(), err
		}
		truth, err := requireBool(cond)
		if err != nil {
			return VoidValue(), err
		}
		if truth {
			return i.evalExpr(frame, n.Value)
		}
		return i.evalExpr(frame, n.Alt)
	case *ast.StructLitExpr:
		if i != nil && i.result != nil && i.result.InitCalls != nil {
			if call, ok := i.result.InitCalls[n]; ok && call != nil {
				return i.evalExpr(frame, call)
			}
		}
		decl, ok := i.lookupStructDecl(n.Name)
		if !ok || decl == nil {
			return VoidValue(), fmt.Errorf("unknown struct %q", n.Name)
		}
		args := make([]Value, 0, len(n.Args))
		for _, arg := range n.Args {
			value, err := i.evalExpr(frame, arg)
			if err != nil {
				return VoidValue(), err
			}
			args = append(args, value)
		}
		return i.constructStruct(decl, args, nil)
	case *ast.ParenExpr:
		return i.evalExpr(frame, n.Inner)
	case *ast.LambdaExpr:
		captures := map[string]Value{}
		if i != nil && i.result != nil && i.result.Lambdas != nil {
			if info, ok := i.result.Lambdas[n]; ok && info != nil {
				for _, name := range info.Captures {
					value, err := i.lookupValue(frame, name)
					if err != nil {
						return VoidValue(), err
					}
					captures[name] = value
				}
			}
		}
		i.nextLambdaID++
		return LambdaFunctionValue(i.nextLambdaID, n, captures), nil
	case *ast.SpecializeExpr:
		name, err := callableName(n.Operand)
		if err != nil {
			return VoidValue(), err
		}
		return FunctionValue(name), nil
	case *ast.ExprBlock:
		blockFrame := childFrame(frame)
		for _, stmt := range n.Stmts {
			signal, err := i.execStmt(blockFrame, stmt)
			if err != nil {
				return VoidValue(), err
			}
			if signal.kind != signalNone {
				return VoidValue(), fmt.Errorf("expression block does not support control transfer")
			}
		}
		return i.evalExpr(blockFrame, n.Value)
	case *ast.CanExpr:
		return i.evalExpr(frame, n.Expr)
	default:
		return VoidValue(), fmt.Errorf("unsupported interpreter expression %T", expr)
	}
}
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
