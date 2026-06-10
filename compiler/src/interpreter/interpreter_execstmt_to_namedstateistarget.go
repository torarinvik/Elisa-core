package interpreter

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (i *Interpreter) execStmt(frame *frame, stmt ast.Stmt) (controlSignal, error) {
	if err := i.debugRecord(frame, DebugBeforeStmt, stmt); err != nil {
		return controlSignal{}, err
	}
	signal, err := i.execStmtCore(frame, stmt)
	if err != nil {
		return controlSignal{}, err
	}
	if err := i.debugRecord(frame, DebugAfterStmt, stmt); err != nil {
		return controlSignal{}, err
	}
	return signal, nil
}

func (i *Interpreter) execStmtCore(frame *frame, stmt ast.Stmt) (controlSignal, error) {
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
	case *ast.RegionStmt:
		// The interpreter has its own (GC'd) memory model, so a region scope — whether
		// written explicitly or synthesized by inference (`in auto:`, the per-function auto
		// region) — is just a transparent block: run its body, no separate arena.
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
		// Prefer the analyzer's recorded resolution so namespaced / using / imported
		// names resolve (the interpreter has no namespace/using context of its own).
		name := n.Name
		if i.result != nil && i.result.ResolvedValueNames != nil {
			if canonical, ok := i.result.ResolvedValueNames[n]; ok {
				name = canonical
			}
		}
		value, err := i.lookupValue(frame, name)
		if err != nil {
			return VoidValue(), err
		}
		return derefValue(value), nil
	case *ast.RaiseExpr:
		// `raise e` returns e through the function's error union. It diverges: evaluate the
		// error, let the debugger break on it, then unwind via a raiseSignal that the program
		// boundary turns into the result.
		raised, err := i.evalExpr(frame, n.Error)
		if err != nil {
			return VoidValue(), err
		}
		if haltErr := i.debugRaise(frame, n, raised); haltErr != nil {
			return VoidValue(), haltErr
		}
		return VoidValue(), &raiseSignal{value: raised}
	case *ast.ShorthandMemberExpr:
		if i != nil && i.result != nil && i.result.ExprTypes != nil {
			if t, ok := i.result.ExprTypes[n]; ok {
				if ce, ok := t.(*semantic.ConstEnumType); ok && ce != nil {
					name := strings.Join(n.Parts, ".")
					if member, ok := ce.Member(name); ok && member != nil {
						return IntValue(member.Value), nil
					}
				}
			}
		}
		return VoidValue(), fmt.Errorf("shorthand member %q has no resolved const enum type", strings.Join(n.Parts, "."))
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
		if i != nil && i.result != nil && i.result.ExprTypes != nil {
			if typ, ok := i.result.ExprTypes[n]; ok {
				return i.zeroValueForSemanticType(typ)
			}
		}
		return VoidValue(), fmt.Errorf("%s: zeroed literal requires typed context", n.Pos())
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
		if i != nil && i.result != nil && i.result.ExprTypes != nil {
			if t, ok := i.result.ExprTypes[n]; ok {
				switch et := t.(type) {
				case *semantic.ConstEnumType:
					if et != nil {
						if member, ok := et.Member(n.Field); ok && member != nil {
							return IntValue(member.Value), nil
						}
					}
				case *semantic.ErrorSetType:
					// A qualified error tag (MyError.Bad) is surfaced as its name so a value
					// raised through an error union is legible in the debugger. Payload data is
					// not yet modeled in the interpreter.
					if name, ok := interpreterQualifiedFieldName(n); ok {
						return StringValue(name), nil
					}
					return StringValue(n.Field), nil
				}
			}
		}
		if name, ok := interpreterQualifiedFieldName(n); ok {
			for _, candidate := range interpreterVisibleNames(frame, name) {
				if value, ok := i.consts[candidate]; ok {
					return value.Clone(), nil
				}
				if value, ok := i.globals[candidate]; ok {
					return value.Clone(), nil
				}
				if _, ok := i.runtimeFuncs[candidate]; ok {
					return FunctionValue(candidate), nil
				}
				if _, ok := i.functions[candidate]; ok {
					return FunctionValue(candidate), nil
				}
				if _, ok := i.lookupStructDecl(candidate); ok {
					return FunctionValue(candidate), nil
				}
			}
		}
		if n.Safe {
			obj, err := i.evalExpr(frame, n.Object)
			if err != nil {
				return VoidValue(), err
			}
			obj = derefValue(obj)
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
		obj = derefValue(obj)
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
		obj = derefValue(obj)
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
		if i != nil && i.result != nil && i.result.PostfixShorthandCalls != nil {
			if call, ok := i.result.PostfixShorthandCalls[n]; ok && call != nil {
				return i.evalExpr(frame, call)
			}
		}
		if _, ok := n.Operand.(*ast.ZeroedLit); ok {
			return i.zeroValueForType(n.Target)
		}
		value, err := i.evalExpr(frame, n.Operand)
		if err != nil {
			return VoidValue(), err
		}
		return i.castValue(value, n.Target)
	case *ast.AddrOfExpr:
		slot, err := i.resolveSlot(frame, n.Operand)
		if err != nil {
			return VoidValue(), err
		}
		return RefValue(slot), nil
	case *ast.GetExpr:
		// `get arr[i] else <value>` is a bounds-checked access. When the value
		// fallback is already on the inner IndexExpr (postfix `else <value>`),
		// delegate to the index evaluation which honors it. When the fallback is
		// a value recovery carried by the `get`, synthesize an equivalent
		// IndexExpr. Optional unwrap and return/raise recovery are not supported
		// by this limited interpreter (it does not model optionals).
		if idx, ok := n.Value.(*ast.IndexExpr); ok {
			if idx.Fallback != nil {
				return i.evalExpr(frame, idx)
			}
			if n.Fallback != nil {
				synth := &ast.IndexExpr{Position: idx.Position, Object: idx.Object, Index: idx.Index, Fallback: n.Fallback}
				return i.evalExpr(frame, synth)
			}
		}
		if slice, ok := n.Value.(*ast.SliceExpr); ok {
			obj, err := i.evalExpr(frame, slice.Object)
			if err != nil {
				return VoidValue(), err
			}
			startV, err := i.evalExpr(frame, slice.Start)
			if err != nil {
				return VoidValue(), err
			}
			endV, err := i.evalExpr(frame, slice.End)
			if err != nil {
				return VoidValue(), err
			}
			start, err := requireInt(startV)
			if err != nil {
				return VoidValue(), err
			}
			end, err := requireInt(endV)
			if err != nil {
				return VoidValue(), err
			}
			length, err := valueSequenceLength(obj)
			if err != nil {
				return VoidValue(), err
			}
			if start >= 0 && start <= end && end <= length {
				return sliceValue(obj, start, end)
			}
			// Out of range: a value fallback handles it; return/raise recovery is
			// not modeled by this limited interpreter.
			if n.Fallback != nil {
				return i.evalExpr(frame, n.Fallback)
			}
		}
		return VoidValue(), fmt.Errorf("unsupported interpreter expression %T", expr)
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
