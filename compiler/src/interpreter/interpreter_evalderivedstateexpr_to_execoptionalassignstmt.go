package interpreter

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/semantic"
	"strconv"
	"unicode/utf8"
)

func (i *Interpreter) evalDerivedStateExpr(frame *frame, expr ast.Expr, self Value) (Value, error) {
	if path, ok := interpreterDerivedStateSelfPath(expr); ok {
		return i.evalDerivedStateSelfPath(self, path)
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return i.evalDerivedStateExpr(frame, n.Inner, self)
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
	case *ast.UnaryExpr:
		operand, err := i.evalDerivedStateExpr(frame, n.Operand, self)
		if err != nil {
			return VoidValue(), err
		}
		return evalUnaryOp(n.Op, operand)
	case *ast.BinaryExpr:
		if n.LoweredCall != nil {
			return i.evalExpr(frame, n.LoweredCall)
		}
		left, err := i.evalDerivedStateExpr(frame, n.Left, self)
		if err != nil {
			return VoidValue(), err
		}
		right, err := i.evalDerivedStateExpr(frame, n.Right, self)
		if err != nil {
			return VoidValue(), err
		}
		return evalBinaryOp(n.Op, left, right)
	default:
		return VoidValue(), fmt.Errorf("unsupported derived-state expression %T", expr)
	}
}
func (i *Interpreter) evalDerivedStateSelfPath(self Value, path []string) (Value, error) {
	value := self
	if len(path) == 0 {
		return value.Clone(), nil
	}
	for _, field := range path {
		if value.kind != valueStruct || value.structVal == nil {
			return VoidValue(), fmt.Errorf("derived-state field access requires a struct value")
		}
		next, ok := value.structVal.Fields[field]
		if !ok {
			return VoidValue(), fmt.Errorf("struct %s has no field %q", value.structVal.Name, field)
		}
		value = next
	}
	return value.Clone(), nil
}
func interpreterDerivedStateSelfPath(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return nil, true
		}
		return nil, false
	case *ast.FieldExpr:
		path, ok := interpreterDerivedStateSelfPath(n.Object)
		if !ok {
			return nil, false
		}
		return append(path, n.Field), true
	case *ast.ParenExpr:
		return interpreterDerivedStateSelfPath(n.Inner)
	default:
		return nil, false
	}
}
func (i *Interpreter) evalCallExpr(frame *frame, expr *ast.CallExpr) (Value, error) {
	if expr != nil && expr.Safe {
		return i.evalSafeCallExpr(frame, expr)
	}
	calleeValue, err := i.evalExpr(frame, expr.Func)
	if err != nil {
		return VoidValue(), err
	}
	return i.invokeCallValue(frame, expr, calleeValue, expr.LoweredArgs(), false)
}
func (i *Interpreter) evalSafeCallExpr(frame *frame, expr *ast.CallExpr) (Value, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return VoidValue(), fmt.Errorf("optional call requires member-call syntax")
	}
	receiverValue, err := i.evalExpr(frame, fieldExpr.Object)
	if err != nil {
		return VoidValue(), err
	}
	if receiverValue.IsNull() {
		if i.safeCallReturnsVoid(expr) {
			return VoidValue(), nil
		}
		return NullValue(), nil
	}
	if i != nil && i.result != nil && i.result.SafeCalls != nil {
		if info, ok := i.result.SafeCalls[expr]; ok && info != nil && info.ResolvedFuncName != "" {
			args := make([]Value, 0, 1+len(info.TailArgs)+len(info.ImplicitArgs))
			args = append(args, receiverValue.Clone())
			for _, argExpr := range info.TailArgs {
				value, err := i.evalExpr(frame, argExpr)
				if err != nil {
					return VoidValue(), err
				}
				args = append(args, value)
			}
			for _, argExpr := range info.ImplicitArgs {
				value, err := i.evalExpr(frame, argExpr)
				if err != nil {
					return VoidValue(), err
				}
				args = append(args, value)
			}
			return i.callFunctionByName(info.ResolvedFuncName, args)
		}
	}
	calleeValue, err := interpreterFieldValue(receiverValue, fieldExpr.Field)
	if err != nil {
		return VoidValue(), err
	}
	return i.invokeCallValue(frame, expr, calleeValue, expr.LoweredArgs(), false)
}
func (i *Interpreter) invokeCallValue(frame *frame, expr *ast.CallExpr, calleeValue Value, argExprs []ast.Expr, prependNamed bool) (Value, error) {
	if calleeValue.kind != valueFunction {
		return VoidValue(), fmt.Errorf("cannot call non-function value %s", calleeValue.String())
	}
	if calleeValue.lambdaVal != nil {
		return i.callLambda(calleeValue.lambdaVal, expr, frame, argExprs, prependNamed)
	}
	name := calleeValue.funcName
	positional := make([]Value, 0, len(argExprs))
	named := map[string]Value{}
	for index, argExpr := range argExprs {
		value, err := i.evalExpr(frame, argExpr)
		if err != nil {
			return VoidValue(), err
		}
		if !prependNamed {
			if name := expr.ArgName(index); name != "" {
				named[name] = value
			} else {
				positional = append(positional, value)
			}
			continue
		}
		if name := expr.ArgName(index + 1); name != "" {
			named[name] = value
		} else {
			positional = append(positional, value)
		}
	}
	if runtimeFn, ok := i.runtimeFuncs[name]; ok {
		if len(named) == 0 {
			return runtimeFn.fn(positional)
		}
		bound := map[string]Value{}
		if err := bindCallArgs(bound, runtimeFn.params, positional, named); err != nil {
			return VoidValue(), fmt.Errorf("%s: %w", expr.Pos(), err)
		}
		ordered := make([]Value, 0, len(runtimeFn.params))
		for _, param := range runtimeFn.params {
			ordered = append(ordered, bound[param.Name].Clone())
		}
		return runtimeFn.fn(ordered)
	}
	if decl, ok := i.functions[name]; ok && decl != nil {
		return i.callFunction(decl, positional, named)
	}
	if decl, ok := i.lookupStructDecl(name); ok && decl != nil {
		if len(named) != 0 {
			return i.constructStruct(decl, positional, named)
		}
		return i.constructStruct(decl, positional, nil)
	}
	return VoidValue(), fmt.Errorf("unknown callable %q", name)
}
func (i *Interpreter) callLambda(lambda *lambdaValue, expr *ast.CallExpr, callerFrame *frame, argExprs []ast.Expr, prependNamed bool) (Value, error) {
	if lambda == nil || lambda.expr == nil {
		return VoidValue(), fmt.Errorf("missing lambda value")
	}
	positional := make([]Value, 0, len(argExprs))
	named := map[string]Value{}
	for index, argExpr := range argExprs {
		value, err := i.evalExpr(callerFrame, argExpr)
		if err != nil {
			return VoidValue(), err
		}
		if !prependNamed {
			if name := expr.ArgName(index); name != "" {
				named[name] = value
			} else {
				positional = append(positional, value)
			}
			continue
		}
		if name := expr.ArgName(index + 1); name != "" {
			named[name] = value
		} else {
			positional = append(positional, value)
		}
	}
	callFrame := &frame{locals: map[string]Value{}}
	for name, value := range lambda.captures {
		callFrame.locals[name] = value.Clone()
	}
	if err := bindCallArgs(callFrame.locals, lambda.expr.Params, positional, named); err != nil {
		return VoidValue(), fmt.Errorf("%s: %w", lambda.expr.Pos(), err)
	}
	if lambda.expr.BodyExpr != nil {
		return i.evalExpr(callFrame, lambda.expr.BodyExpr)
	}
	signal, err := i.execBlock(callFrame, lambda.expr.Body)
	if err != nil {
		return VoidValue(), err
	}
	if signal.kind == signalReturn {
		return signal.value, nil
	}
	if i.lambdaReturnsVoid(lambda.expr) {
		return VoidValue(), nil
	}
	return VoidValue(), fmt.Errorf("%s: reached end of lambda without return", lambda.expr.Pos())
}
func (i *Interpreter) lambdaReturnsVoid(expr *ast.LambdaExpr) bool {
	if expr == nil || i == nil || i.result == nil || i.result.ExprTypes == nil {
		return false
	}
	fnType, ok := i.result.ExprTypes[expr].(*semantic.FuncType)
	if !ok || fnType == nil {
		return false
	}
	return semantic.SameType(fnType.Return, i.result.NamedTypes["void"])
}
func interpreterFieldValue(obj Value, field string) (Value, error) {
	if obj.kind != valueStruct || obj.structVal == nil {
		return VoidValue(), fmt.Errorf("field access requires a struct, got %s", obj.String())
	}
	value, ok := obj.structVal.Fields[field]
	if !ok {
		return VoidValue(), fmt.Errorf("struct %s has no field %q", obj.structVal.Name, field)
	}
	return value, nil
}
func (i *Interpreter) safeCallReturnsVoid(expr *ast.CallExpr) bool {
	if i == nil || i.result == nil || i.result.ExprTypes == nil || expr == nil {
		return false
	}
	return semantic.SameType(i.result.ExprTypes[expr], i.result.NamedTypes["void"])
}
func callableName(expr ast.Expr) (string, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, nil
	case *ast.SpecializeExpr:
		return callableName(n.Operand)
	default:
		return "", fmt.Errorf("unsupported callable %T", expr)
	}
}
func (i *Interpreter) lookupStructDecl(name string) (*ast.StructDecl, bool) {
	if i == nil || name == "" {
		return nil, false
	}
	if decl, ok := i.structs[name]; ok && decl != nil {
		return decl, true
	}
	activeFile := i.result.ActiveFile()
	if i.result == nil || activeFile == nil {
		return nil, false
	}
	var search func([]ast.Decl) (*ast.StructDecl, bool)
	search = func(decls []ast.Decl) (*ast.StructDecl, bool) {
		for _, decl := range decls {
			switch n := decl.(type) {
			case *ast.StructDecl:
				if n != nil && n.Name == name {
					return n, true
				}
			case *ast.NamespaceDecl:
				if found, ok := search(n.Decls); ok {
					return found, true
				}
			}
		}
		return nil, false
	}
	decl, ok := search(activeFile.Decls)
	if !ok || decl == nil {
		return nil, false
	}
	i.structs[name] = decl
	return decl, true
}
func (i *Interpreter) constructStruct(decl *ast.StructDecl, positional []Value, named map[string]Value) (Value, error) {
	if decl == nil {
		return VoidValue(), fmt.Errorf("struct declaration is nil")
	}
	fields := map[string]Value{}
	fieldOrder := make([]string, 0, len(decl.Fields))
	fieldDecls := make(map[string]ast.FieldDecl, len(decl.Fields))
	for _, field := range decl.Fields {
		fieldOrder = append(fieldOrder, field.Name)
		fieldDecls[field.Name] = field
	}
	if len(named) == 0 {
		if len(positional) != len(decl.Fields) {
			return VoidValue(), fmt.Errorf("struct %q expects %d arguments, got %d", decl.Name, len(decl.Fields), len(positional))
		}
		for index, field := range decl.Fields {
			fields[field.Name] = positional[index].Clone()
		}
		return StructInstanceValue(decl.Name, fieldOrder, fields), nil
	}
	if len(positional) > len(decl.Fields) {
		return VoidValue(), fmt.Errorf("struct %q received too many positional arguments", decl.Name)
	}
	used := map[string]bool{}
	for index, value := range positional {
		field := decl.Fields[index]
		fields[field.Name] = value.Clone()
		used[field.Name] = true
	}
	for name, value := range named {
		if _, ok := fieldDecls[name]; !ok {
			return VoidValue(), fmt.Errorf("struct %q has no field %q", decl.Name, name)
		}
		if used[name] {
			return VoidValue(), fmt.Errorf("field %q initialized more than once", name)
		}
		fields[name] = value.Clone()
		used[name] = true
	}
	for _, field := range decl.Fields {
		if _, ok := fields[field.Name]; ok {
			continue
		}
		zero, err := i.zeroValueForType(field.Type)
		if err != nil {
			return VoidValue(), fmt.Errorf("missing field %q and no zero value available: %w", field.Name, err)
		}
		fields[field.Name] = zero
	}
	return StructInstanceValue(decl.Name, fieldOrder, fields), nil
}
func (i *Interpreter) lookupValue(frame *frame, name string) (Value, error) {
	for current := frame; current != nil; current = current.parent {
		if value, ok := current.locals[name]; ok {
			return value.Clone(), nil
		}
	}
	if value, ok := i.globals[name]; ok {
		return value.Clone(), nil
	}
	if value, ok := i.consts[name]; ok {
		return value.Clone(), nil
	}
	if _, ok := i.runtimeFuncs[name]; ok {
		return FunctionValue(name), nil
	}
	if _, ok := i.functions[name]; ok {
		return FunctionValue(name), nil
	}
	if _, ok := i.lookupStructDecl(name); ok {
		return FunctionValue(name), nil
	}
	return VoidValue(), fmt.Errorf("undefined name %q", name)
}
func (i *Interpreter) resolveSlot(frame *frame, expr ast.Expr) (*valueSlot, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		name := n.Name
		for current := frame; current != nil; current = current.parent {
			if _, ok := current.locals[name]; ok {
				scopeFrame := current
				return &valueSlot{
					get: func() Value { return scopeFrame.locals[name].Clone() },
					set: func(value Value) error {
						scopeFrame.locals[name] = value.Clone()
						return nil
					},
				}, nil
			}
		}
		if _, ok := i.globals[name]; ok {
			return &valueSlot{
				get: func() Value { return i.globals[name].Clone() },
				set: func(value Value) error {
					i.globals[name] = value.Clone()
					return nil
				},
			}, nil
		}
		return nil, fmt.Errorf("undefined assignable name %q", name)
	case *ast.FieldExpr:
		parent, err := i.resolveSlot(frame, n.Object)
		if err != nil {
			return nil, err
		}
		return &valueSlot{
			get: func() Value {
				parentValue := parent.get()
				if parentValue.kind != valueStruct || parentValue.structVal == nil {
					return VoidValue()
				}
				return parentValue.structVal.Fields[n.Field].Clone()
			},
			set: func(value Value) error {
				parentValue := parent.get()
				if parentValue.kind != valueStruct || parentValue.structVal == nil {
					return fmt.Errorf("field assignment requires a struct")
				}
				if _, ok := parentValue.structVal.Fields[n.Field]; !ok {
					return fmt.Errorf("struct %s has no field %q", parentValue.structVal.Name, n.Field)
				}
				updated := parentValue.Clone()
				updated.structVal.Fields[n.Field] = value.Clone()
				return parent.set(updated)
			},
		}, nil
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return nil, fmt.Errorf("safe index fallback cannot be used as an assignment target")
		}
		parent, err := i.resolveSlot(frame, n.Object)
		if err != nil {
			return nil, err
		}
		indexValue, err := i.evalExpr(frame, n.Index)
		if err != nil {
			return nil, err
		}
		index, err := requireInt(indexValue)
		if err != nil {
			return nil, err
		}
		return &valueSlot{
			get: func() Value {
				value, _ := indexValueAt(parent.get(), index)
				return value
			},
			set: func(value Value) error {
				parentValue := parent.get()
				if parentValue.kind != valueList {
					return fmt.Errorf("index assignment requires a list")
				}
				if index < 0 || int(index) >= len(parentValue.listVal) {
					return fmt.Errorf("index %d out of range", index)
				}
				updated := parentValue.Clone()
				updated.listVal[index] = value.Clone()
				return parent.set(updated)
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported assignment target %T", expr)
	}
}
func stripOptionalAssignInterpreterTarget(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok || paren == nil {
			return expr
		}
		expr = paren.Inner
	}
}
func (i *Interpreter) execOptionalAssignStmt(frame *frame, stmt *ast.AssignStmt) error {
	if stmt == nil || stmt.Target == nil || stmt.Value == nil {
		return fmt.Errorf("invalid ?= statement")
	}
	value, err := i.evalExpr(frame, stmt.Value)
	if err != nil {
		return err
	}
	switch target := stripOptionalAssignInterpreterTarget(stmt.Target).(type) {
	case *ast.Ident:
		slot, err := i.resolveSlot(frame, target)
		if err != nil {
			return err
		}
		if slot.get().IsNull() {
			return nil
		}
		return slot.set(value)
	case *ast.FieldExpr:
		if !target.Safe {
			return fmt.Errorf("?= requires optional chaining on field targets")
		}
		parentValue, err := i.evalExpr(frame, target.Object)
		if err != nil {
			return err
		}
		if parentValue.IsNull() {
			return nil
		}
		if parentValue.kind != valueStruct || parentValue.structVal == nil {
			return fmt.Errorf("field assignment requires a struct")
		}
		if _, ok := parentValue.structVal.Fields[target.Field]; !ok {
			return fmt.Errorf("struct %s has no field %q", parentValue.structVal.Name, target.Field)
		}
		parentSlot, err := i.resolveSlot(frame, target.Object)
		if err != nil {
			return err
		}
		updated := parentValue.Clone()
		updated.structVal.Fields[target.Field] = value.Clone()
		return parentSlot.set(updated)
	default:
		return fmt.Errorf("invalid ?= target")
	}
}
