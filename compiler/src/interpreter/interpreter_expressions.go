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
		if obj.kind == valueDict {
			found := dictLookup(obj, index)
			if found.IsNull() && n.Fallback != nil {
				return i.evalExpr(frame, n.Fallback)
			}
			return found, nil
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
		if i.isDictLiteral(n) {
			return i.evalDictLiteral(frame, n)
		}
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
		// Prefer the analyzer's field-ordered arguments: they place named
		// fields by name and include filled-in defaults and desugared
		// `..base` copies, which the raw source-order Args do not.
		argExprs := n.Args
		if n.ResolvedArgsValid && len(n.ResolvedArgs) == len(decl.Fields) {
			complete := true
			for _, arg := range n.ResolvedArgs {
				if arg == nil {
					complete = false
					break
				}
			}
			if complete {
				argExprs = n.ResolvedArgs
			}
		}
		args := make([]Value, 0, len(argExprs))
		for _, arg := range argExprs {
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
