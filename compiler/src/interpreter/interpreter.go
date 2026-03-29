package interpreter

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

const maxLoopIterations = 1_000_000

type Options struct {
	Entry  string
	Stdout io.Writer
}

type Result struct {
	Return Value
	Stdout string
}

type valueKind int

const (
	valueVoid valueKind = iota
	valueNull
	valueInt
	valueFloat
	valueBool
	valueString
	valueList
	valueStruct
	valueFunction
)

type Value struct {
	kind      valueKind
	int64Val  int64
	floatVal  float64
	boolVal   bool
	strVal    string
	listVal   []Value
	structVal *StructValue
	funcName  string
}

type StructValue struct {
	Name       string
	FieldOrder []string
	Fields     map[string]Value
}

type runtimeFunc func([]Value) (Value, error)

type Interpreter struct {
	result       *semantic.Result
	stdout       io.Writer
	functions    map[string]*ast.FuncDecl
	structs      map[string]*ast.StructDecl
	consts       map[string]Value
	globals      map[string]Value
	runtimeFuncs map[string]runtimeFunc
}

type frame struct {
	locals map[string]Value
}

type signalKind int

const (
	signalNone signalKind = iota
	signalReturn
)

type controlSignal struct {
	kind  signalKind
	value Value
}

type valueSlot struct {
	get func() Value
	set func(Value) error
}

func Execute(result *semantic.Result, options Options) (*Result, error) {
	if result == nil || result.File == nil {
		return nil, fmt.Errorf("interpreter requires a semantic result with an AST")
	}
	var stdout bytes.Buffer
	writer := io.Writer(&stdout)
	if options.Stdout != nil {
		writer = io.MultiWriter(&stdout, options.Stdout)
	}
	interp := &Interpreter{
		result:    result,
		stdout:    writer,
		functions: map[string]*ast.FuncDecl{},
		structs:   map[string]*ast.StructDecl{},
		consts:    map[string]Value{},
		globals:   map[string]Value{},
	}
	interp.runtimeFuncs = map[string]runtimeFunc{
		"puts":    interp.runtimePuts,
		"rt_puts": interp.runtimePuts,
		"assert":  interp.runtimeAssert,
	}
	if err := interp.bootstrap(); err != nil {
		return nil, err
	}
	entryName := strings.TrimSpace(options.Entry)
	if entryName == "" {
		entryName = interp.defaultEntryName()
	}
	returnValue, err := interp.callFunctionByName(entryName, nil)
	if err != nil {
		return nil, err
	}
	return &Result{Return: returnValue, Stdout: stdout.String()}, nil
}

func (v Value) IsVoid() bool {
	return v.kind == valueVoid
}

func (v Value) IsNull() bool {
	return v.kind == valueNull
}

func (v Value) String() string {
	switch v.kind {
	case valueVoid:
		return "void"
	case valueNull:
		return "null"
	case valueInt:
		return strconv.FormatInt(v.int64Val, 10)
	case valueFloat:
		return strconv.FormatFloat(v.floatVal, 'g', -1, 64)
	case valueBool:
		if v.boolVal {
			return "true"
		}
		return "false"
	case valueString:
		return v.strVal
	case valueList:
		parts := make([]string, 0, len(v.listVal))
		for _, elem := range v.listVal {
			parts = append(parts, elem.String())
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case valueStruct:
		if v.structVal == nil {
			return "<invalid-struct>"
		}
		parts := make([]string, 0, len(v.structVal.FieldOrder))
		for _, name := range v.structVal.FieldOrder {
			parts = append(parts, name+": "+v.structVal.Fields[name].String())
		}
		return v.structVal.Name + "(" + strings.Join(parts, ", ") + ")"
	case valueFunction:
		return "<func " + v.funcName + ">"
	default:
		return "<value>"
	}
}

func (v Value) Clone() Value {
	cloned := v
	switch v.kind {
	case valueList:
		cloned.listVal = make([]Value, len(v.listVal))
		for i, elem := range v.listVal {
			cloned.listVal[i] = elem.Clone()
		}
	case valueStruct:
		if v.structVal != nil {
			fields := make(map[string]Value, len(v.structVal.Fields))
			for name, value := range v.structVal.Fields {
				fields[name] = value.Clone()
			}
			cloned.structVal = &StructValue{
				Name:       v.structVal.Name,
				FieldOrder: append([]string(nil), v.structVal.FieldOrder...),
				Fields:     fields,
			}
		}
	}
	return cloned
}

func VoidValue() Value                { return Value{kind: valueVoid} }
func NullValue() Value                { return Value{kind: valueNull} }
func IntValue(v int64) Value          { return Value{kind: valueInt, int64Val: v} }
func FloatValue(v float64) Value      { return Value{kind: valueFloat, floatVal: v} }
func BoolValue(v bool) Value          { return Value{kind: valueBool, boolVal: v} }
func StringValue(v string) Value      { return Value{kind: valueString, strVal: v} }
func FunctionValue(name string) Value { return Value{kind: valueFunction, funcName: name} }

func ListValue(values []Value) Value {
	cloned := make([]Value, len(values))
	for i, value := range values {
		cloned[i] = value.Clone()
	}
	return Value{kind: valueList, listVal: cloned}
}

func StructInstanceValue(name string, fieldOrder []string, fields map[string]Value) Value {
	clonedFields := make(map[string]Value, len(fields))
	for fieldName, value := range fields {
		clonedFields[fieldName] = value.Clone()
	}
	return Value{kind: valueStruct, structVal: &StructValue{Name: name, FieldOrder: append([]string(nil), fieldOrder...), Fields: clonedFields}}
}

func (i *Interpreter) bootstrap() error {
	if i.result.GlobalScope != nil {
		for name, sym := range i.result.GlobalScope.Symbols {
			if sym == nil {
				continue
			}
			switch sym.Kind {
			case semantic.SymbolFunc:
				if decl, ok := sym.Node.(*ast.FuncDecl); ok && decl != nil {
					i.functions[name] = decl
				}
			case semantic.SymbolStruct:
				if decl, ok := sym.Node.(*ast.StructDecl); ok && decl != nil {
					i.structs[name] = decl
				}
			}
		}
	}
	for name, value := range i.result.ConstValues {
		i.consts[name] = constValueToValue(value)
	}
	for _, decl := range i.result.File.Decls {
		if err := i.initializeGlobalsFromDecl(decl); err != nil {
			return err
		}
	}
	return nil
}

func constValueToValue(value semantic.ConstValue) Value {
	switch value.Kind {
	case semantic.ConstInt:
		return IntValue(value.Int)
	case semantic.ConstFloat:
		return FloatValue(value.Float)
	case semantic.ConstBool:
		return BoolValue(value.Bool)
	case semantic.ConstString:
		return StringValue(value.String)
	default:
		return VoidValue()
	}
}

func (i *Interpreter) initializeGlobalsFromDecl(decl ast.Decl) error {
	switch n := decl.(type) {
	case *ast.GlobalDecl:
		value, err := i.evaluateInitializer(nil, n.Type, n.Value)
		if err != nil {
			return err
		}
		i.globals[n.Name] = value
	case *ast.NamespaceDecl:
		for _, nested := range n.Decls {
			if err := i.initializeGlobalsFromDecl(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func (i *Interpreter) defaultEntryName() string {
	for _, exported := range i.result.ExportedFuncs {
		if exported != nil && exported.PublicName == "main" && exported.TargetName != "" {
			return exported.TargetName
		}
	}
	if _, ok := i.functions["main"]; ok {
		return "main"
	}
	return "main"
}

func (i *Interpreter) runtimePuts(args []Value) (Value, error) {
	if len(args) != 1 {
		return VoidValue(), fmt.Errorf("puts expects 1 argument, got %d", len(args))
	}
	text, err := stringifyValue(args[0])
	if err != nil {
		return VoidValue(), err
	}
	_, _ = io.WriteString(i.stdout, text)
	return IntValue(int64(len(text))), nil
}

func (i *Interpreter) runtimeAssert(args []Value) (Value, error) {
	if len(args) != 1 {
		return VoidValue(), fmt.Errorf("assert expects 1 argument, got %d", len(args))
	}
	cond, err := requireBool(args[0])
	if err != nil {
		return VoidValue(), err
	}
	if !cond {
		return VoidValue(), fmt.Errorf("panic: assert failed")
	}
	return VoidValue(), nil
}

func stringifyValue(value Value) (string, error) {
	switch value.kind {
	case valueString:
		return value.strVal, nil
	case valueNull:
		return "null", nil
	case valueInt, valueFloat, valueBool:
		return value.String(), nil
	default:
		return "", fmt.Errorf("cannot stringify %s", value.String())
	}
}

func (i *Interpreter) callFunctionByName(name string, args []Value) (Value, error) {
	if runtimeFn, ok := i.runtimeFuncs[name]; ok {
		return runtimeFn(cloneArgs(args))
	}
	fn, ok := i.functions[name]
	if !ok || fn == nil {
		return VoidValue(), fmt.Errorf("interpreter does not know function %q", name)
	}
	return i.callFunction(fn, args, nil)
}

func cloneArgs(args []Value) []Value {
	cloned := make([]Value, len(args))
	for i, arg := range args {
		cloned[i] = arg.Clone()
	}
	return cloned
}

func (i *Interpreter) callFunction(fn *ast.FuncDecl, positional []Value, named map[string]Value) (Value, error) {
	frame := &frame{locals: map[string]Value{}}
	if err := bindCallArgs(frame.locals, fn.Params, positional, named); err != nil {
		return VoidValue(), fmt.Errorf("%s: %w", fn.Pos(), err)
	}
	signal, err := i.execBlock(frame, fn.Body)
	if err != nil {
		return VoidValue(), err
	}
	if signal.kind == signalReturn {
		return signal.value, nil
	}
	if isVoidTypeExpr(fn.ReturnType) {
		return VoidValue(), nil
	}
	return VoidValue(), fmt.Errorf("%s: reached end of function %q without return", fn.Pos(), fn.Name)
}

func bindCallArgs(dst map[string]Value, params []ast.ParamDecl, positional []Value, named map[string]Value) error {
	if len(named) == 0 {
		if len(positional) != len(params) {
			return fmt.Errorf("expected %d arguments, got %d", len(params), len(positional))
		}
		for i, param := range params {
			dst[param.Name] = positional[i].Clone()
		}
		return nil
	}
	if len(positional) > len(params) {
		return fmt.Errorf("expected at most %d positional arguments, got %d", len(params), len(positional))
	}
	used := map[string]bool{}
	for i, arg := range positional {
		if i >= len(params) {
			return fmt.Errorf("too many positional arguments")
		}
		used[params[i].Name] = true
		dst[params[i].Name] = arg.Clone()
	}
	for name, value := range named {
		if used[name] {
			return fmt.Errorf("argument %q provided more than once", name)
		}
		found := false
		for _, param := range params {
			if param.Name == name {
				dst[name] = value.Clone()
				used[name] = true
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown argument %q", name)
		}
	}
	for _, param := range params {
		if _, ok := dst[param.Name]; !ok {
			return fmt.Errorf("missing argument %q", param.Name)
		}
	}
	return nil
}

func (i *Interpreter) execBlock(frame *frame, body []ast.Stmt) (controlSignal, error) {
	for _, stmt := range body {
		signal, err := i.execStmt(frame, stmt)
		if err != nil {
			return controlSignal{}, err
		}
		if signal.kind != signalNone {
			return signal, nil
		}
	}
	return controlSignal{}, nil
}

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
		decl, ok := i.structs[n.Name]
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
	case *ast.SpecializeExpr:
		name, err := callableName(n.Operand)
		if err != nil {
			return VoidValue(), err
		}
		return FunctionValue(name), nil
	case *ast.CanExpr:
		return i.evalExpr(frame, n.Expr)
	default:
		return VoidValue(), fmt.Errorf("unsupported interpreter expression %T", expr)
	}
}

func (i *Interpreter) evalCallExpr(frame *frame, expr *ast.CallExpr) (Value, error) {
	name, err := callableName(expr.Func)
	if err != nil {
		calleeValue, valueErr := i.evalExpr(frame, expr.Func)
		if valueErr != nil {
			return VoidValue(), err
		}
		if calleeValue.kind != valueFunction {
			return VoidValue(), err
		}
		name = calleeValue.funcName
	}
	positional := make([]Value, 0, len(expr.Args))
	named := map[string]Value{}
	for index, argExpr := range expr.Args {
		value, err := i.evalExpr(frame, argExpr)
		if err != nil {
			return VoidValue(), err
		}
		if name := expr.ArgName(index); name != "" {
			named[name] = value
		} else {
			positional = append(positional, value)
		}
	}
	if runtimeFn, ok := i.runtimeFuncs[name]; ok {
		if len(named) != 0 {
			return VoidValue(), fmt.Errorf("runtime function %q does not support named arguments", name)
		}
		return runtimeFn(positional)
	}
	if decl, ok := i.functions[name]; ok && decl != nil {
		return i.callFunction(decl, positional, named)
	}
	if decl, ok := i.structs[name]; ok && decl != nil {
		if len(named) != 0 {
			return i.constructStruct(decl, positional, named)
		}
		return i.constructStruct(decl, positional, nil)
	}
	return VoidValue(), fmt.Errorf("unknown callable %q", name)
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
	if frame != nil {
		if value, ok := frame.locals[name]; ok {
			return value.Clone(), nil
		}
	}
	if value, ok := i.globals[name]; ok {
		return value.Clone(), nil
	}
	if value, ok := i.consts[name]; ok {
		return value.Clone(), nil
	}
	if _, ok := i.functions[name]; ok {
		return FunctionValue(name), nil
	}
	if _, ok := i.structs[name]; ok {
		return FunctionValue(name), nil
	}
	return VoidValue(), fmt.Errorf("undefined name %q", name)
}

func (i *Interpreter) resolveSlot(frame *frame, expr ast.Expr) (*valueSlot, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		name := n.Name
		if frame != nil {
			if _, ok := frame.locals[name]; ok {
				return &valueSlot{
					get: func() Value { return frame.locals[name].Clone() },
					set: func(value Value) error {
						frame.locals[name] = value.Clone()
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

func (i *Interpreter) zeroValueForType(typ ast.TypeExpr) (Value, error) {
	switch n := typ.(type) {
	case nil:
		return VoidValue(), nil
	case *ast.NamedType:
		switch n.Name {
		case "void":
			return VoidValue(), nil
		case "bool":
			return BoolValue(false), nil
		case "f32", "f64":
			return FloatValue(0), nil
		case "str", "string":
			return StringValue(""), nil
		case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
			return IntValue(0), nil
		default:
			if decl, ok := i.structs[n.Name]; ok && decl != nil {
				fields := make(map[string]Value, len(decl.Fields))
				order := make([]string, 0, len(decl.Fields))
				for _, field := range decl.Fields {
					zero, err := i.zeroValueForType(field.Type)
					if err != nil {
						return VoidValue(), err
					}
					fields[field.Name] = zero
					order = append(order, field.Name)
				}
				return StructInstanceValue(decl.Name, order, fields), nil
			}
			return VoidValue(), fmt.Errorf("no zero value rule for type %q", n.Name)
		}
	case *ast.MutableType:
		return i.zeroValueForType(n.Elem)
	case *ast.TailType:
		return i.zeroValueForType(n.Elem)
	case *ast.OptionalTypeExpr:
		return NullValue(), nil
	case *ast.RefType:
		return NullValue(), nil
	case *ast.ArrayType:
		size, err := constArraySize(n.Size)
		if err != nil {
			return VoidValue(), err
		}
		values := make([]Value, size)
		for idx := 0; idx < size; idx++ {
			zero, err := i.zeroValueForType(n.Elem)
			if err != nil {
				return VoidValue(), err
			}
			values[idx] = zero
		}
		return ListValue(values), nil
	default:
		return VoidValue(), fmt.Errorf("no zero value rule for type %T", typ)
	}
}

func constArraySize(expr ast.Expr) (int, error) {
	lit, ok := expr.(*ast.IntLit)
	if !ok {
		return 0, fmt.Errorf("array zero-initialization requires a constant integer size")
	}
	value, err := parseIntLiteral(lit)
	if err != nil {
		return 0, err
	}
	if value.kind != valueInt || value.int64Val < 0 {
		return 0, fmt.Errorf("invalid array size %s", value.String())
	}
	return int(value.int64Val), nil
}

func (i *Interpreter) castValue(value Value, target ast.TypeExpr) (Value, error) {
	switch n := target.(type) {
	case *ast.NamedType:
		switch n.Name {
		case "void":
			return VoidValue(), nil
		case "bool":
			switch value.kind {
			case valueBool:
				return value, nil
			case valueInt:
				return BoolValue(value.int64Val != 0), nil
			case valueNull:
				return BoolValue(false), nil
			default:
				return VoidValue(), fmt.Errorf("cannot cast %s to bool", value.String())
			}
		case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
			switch value.kind {
			case valueInt:
				return value, nil
			case valueBool:
				if value.boolVal {
					return IntValue(1), nil
				}
				return IntValue(0), nil
			case valueFloat:
				return IntValue(int64(value.floatVal)), nil
			case valueNull:
				return IntValue(0), nil
			default:
				return VoidValue(), fmt.Errorf("cannot cast %s to %s", value.String(), n.Name)
			}
		case "f32", "f64":
			switch value.kind {
			case valueFloat:
				return value, nil
			case valueInt:
				return FloatValue(float64(value.int64Val)), nil
			case valueBool:
				if value.boolVal {
					return FloatValue(1), nil
				}
				return FloatValue(0), nil
			default:
				return VoidValue(), fmt.Errorf("cannot cast %s to %s", value.String(), n.Name)
			}
		case "str", "string":
			return stringifyOrWrap(value)
		default:
			if value.kind == valueStruct && value.structVal != nil && value.structVal.Name == n.Name {
				return value, nil
			}
			if value.kind == valueString {
				return value, nil
			}
			return value, nil
		}
	case *ast.RefType, *ast.OptionalTypeExpr:
		return value, nil
	case *ast.MutableType:
		return i.castValue(value, n.Elem)
	case *ast.TailType:
		return i.castValue(value, n.Elem)
	default:
		return value, nil
	}
}

func stringifyOrWrap(value Value) (Value, error) {
	text, err := stringifyValue(value)
	if err != nil {
		return VoidValue(), err
	}
	return StringValue(text), nil
}

func parseIntLiteral(lit *ast.IntLit) (Value, error) {
	if lit == nil {
		return VoidValue(), fmt.Errorf("integer literal is nil")
	}
	base := 10
	text := lit.Value
	if lit.IsHex {
		base = 0
	}
	value, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		return VoidValue(), err
	}
	return IntValue(value), nil
}

func evalUnaryOp(op lexer.TokenKind, operand Value) (Value, error) {
	switch op {
	case lexer.TOKEN_NOT:
		value, err := requireBool(operand)
		if err != nil {
			return VoidValue(), err
		}
		return BoolValue(!value), nil
	case lexer.TOKEN_MINUS:
		switch operand.kind {
		case valueInt:
			return IntValue(-operand.int64Val), nil
		case valueFloat:
			return FloatValue(-operand.floatVal), nil
		default:
			return VoidValue(), fmt.Errorf("unary - requires numeric operand, got %s", operand.String())
		}
	case lexer.TOKEN_PLUS:
		switch operand.kind {
		case valueInt, valueFloat:
			return operand, nil
		default:
			return VoidValue(), fmt.Errorf("unary + requires numeric operand, got %s", operand.String())
		}
	case lexer.TOKEN_TILDE:
		value, err := requireInt(operand)
		if err != nil {
			return VoidValue(), err
		}
		return IntValue(^value), nil
	default:
		return VoidValue(), fmt.Errorf("unsupported unary operator %s", lexer.TokenName(op))
	}
}

func augAssignBaseOp(op lexer.TokenKind) lexer.TokenKind {
	switch op {
	case lexer.TOKEN_PLUSEQ:
		return lexer.TOKEN_PLUS
	case lexer.TOKEN_MINUSEQ:
		return lexer.TOKEN_MINUS
	case lexer.TOKEN_STAREQ:
		return lexer.TOKEN_STAR
	case lexer.TOKEN_SLASHEQ:
		return lexer.TOKEN_SLASH
	case lexer.TOKEN_PERCENTEQ:
		return lexer.TOKEN_PERCENT
	case lexer.TOKEN_CARETEQ:
		return lexer.TOKEN_CARET
	case lexer.TOKEN_PIPEEQ:
		return lexer.TOKEN_PIPE
	case lexer.TOKEN_AMPEQ:
		return lexer.TOKEN_AMPERSAND
	case lexer.TOKEN_LSHIFTEQ:
		return lexer.TOKEN_LSHIFT
	case lexer.TOKEN_RSHIFTEQ:
		return lexer.TOKEN_RSHIFT
	default:
		return op
	}
}

func evalBinaryOp(op lexer.TokenKind, left Value, right Value) (Value, error) {
	switch op {
	case lexer.TOKEN_AND:
		l, err := requireBool(left)
		if err != nil {
			return VoidValue(), err
		}
		r, err := requireBool(right)
		if err != nil {
			return VoidValue(), err
		}
		return BoolValue(l && r), nil
	case lexer.TOKEN_OR:
		l, err := requireBool(left)
		if err != nil {
			return VoidValue(), err
		}
		r, err := requireBool(right)
		if err != nil {
			return VoidValue(), err
		}
		return BoolValue(l || r), nil
	case lexer.TOKEN_EQEQ:
		return BoolValue(valuesEqual(left, right)), nil
	case lexer.TOKEN_BANGEQ:
		return BoolValue(!valuesEqual(left, right)), nil
	case lexer.TOKEN_PLUS:
		if left.kind == valueString || right.kind == valueString {
			leftText, err := stringifyValue(left)
			if err != nil {
				return VoidValue(), err
			}
			rightText, err := stringifyValue(right)
			if err != nil {
				return VoidValue(), err
			}
			return StringValue(leftText + rightText), nil
		}
	}
	if left.kind == valueFloat || right.kind == valueFloat {
		return evalFloatBinaryOp(op, left, right)
	}
	leftInt, err := requireInt(left)
	if err != nil {
		return VoidValue(), err
	}
	rightInt, err := requireInt(right)
	if err != nil {
		return VoidValue(), err
	}
	switch op {
	case lexer.TOKEN_PLUS:
		return IntValue(leftInt + rightInt), nil
	case lexer.TOKEN_MINUS:
		return IntValue(leftInt - rightInt), nil
	case lexer.TOKEN_STAR:
		return IntValue(leftInt * rightInt), nil
	case lexer.TOKEN_SLASH:
		if rightInt == 0 {
			return VoidValue(), fmt.Errorf("division by zero")
		}
		return IntValue(leftInt / rightInt), nil
	case lexer.TOKEN_PERCENT:
		if rightInt == 0 {
			return VoidValue(), fmt.Errorf("modulo by zero")
		}
		return IntValue(leftInt % rightInt), nil
	case lexer.TOKEN_LT:
		return BoolValue(leftInt < rightInt), nil
	case lexer.TOKEN_GT:
		return BoolValue(leftInt > rightInt), nil
	case lexer.TOKEN_LTEQ:
		return BoolValue(leftInt <= rightInt), nil
	case lexer.TOKEN_GTEQ:
		return BoolValue(leftInt >= rightInt), nil
	case lexer.TOKEN_PIPE:
		return IntValue(leftInt | rightInt), nil
	case lexer.TOKEN_CARET:
		return IntValue(leftInt ^ rightInt), nil
	case lexer.TOKEN_AMPERSAND:
		return IntValue(leftInt & rightInt), nil
	case lexer.TOKEN_LSHIFT:
		return IntValue(leftInt << rightInt), nil
	case lexer.TOKEN_RSHIFT:
		return IntValue(leftInt >> rightInt), nil
	default:
		return VoidValue(), fmt.Errorf("unsupported binary operator %s", lexer.TokenName(op))
	}
}

func evalFloatBinaryOp(op lexer.TokenKind, left Value, right Value) (Value, error) {
	leftFloat, err := numericAsFloat(left)
	if err != nil {
		return VoidValue(), err
	}
	rightFloat, err := numericAsFloat(right)
	if err != nil {
		return VoidValue(), err
	}
	switch op {
	case lexer.TOKEN_PLUS:
		return FloatValue(leftFloat + rightFloat), nil
	case lexer.TOKEN_MINUS:
		return FloatValue(leftFloat - rightFloat), nil
	case lexer.TOKEN_STAR:
		return FloatValue(leftFloat * rightFloat), nil
	case lexer.TOKEN_SLASH:
		if rightFloat == 0 {
			return VoidValue(), fmt.Errorf("division by zero")
		}
		return FloatValue(leftFloat / rightFloat), nil
	case lexer.TOKEN_LT:
		return BoolValue(leftFloat < rightFloat), nil
	case lexer.TOKEN_GT:
		return BoolValue(leftFloat > rightFloat), nil
	case lexer.TOKEN_LTEQ:
		return BoolValue(leftFloat <= rightFloat), nil
	case lexer.TOKEN_GTEQ:
		return BoolValue(leftFloat >= rightFloat), nil
	default:
		return VoidValue(), fmt.Errorf("unsupported float binary operator %s", lexer.TokenName(op))
	}
}

func requireBool(value Value) (bool, error) {
	if value.kind != valueBool {
		return false, fmt.Errorf("expected bool, got %s", value.String())
	}
	return value.boolVal, nil
}

func requireInt(value Value) (int64, error) {
	if value.kind != valueInt {
		return 0, fmt.Errorf("expected integer, got %s", value.String())
	}
	return value.int64Val, nil
}

func numericAsFloat(value Value) (float64, error) {
	switch value.kind {
	case valueFloat:
		return value.floatVal, nil
	case valueInt:
		return float64(value.int64Val), nil
	default:
		return 0, fmt.Errorf("expected numeric value, got %s", value.String())
	}
}

func indexValue(value Value, index int64) (Value, error) {
	return indexValueAt(value, index)
}

func indexValueAt(value Value, index int64) (Value, error) {
	if index < 0 {
		return VoidValue(), fmt.Errorf("index %d out of range", index)
	}
	switch value.kind {
	case valueList:
		if int(index) >= len(value.listVal) {
			return VoidValue(), fmt.Errorf("index %d out of range", index)
		}
		return value.listVal[index].Clone(), nil
	case valueString:
		if int(index) >= len(value.strVal) {
			return VoidValue(), fmt.Errorf("index %d out of range", index)
		}
		return IntValue(int64(value.strVal[index])), nil
	default:
		return VoidValue(), fmt.Errorf("indexing requires list or string, got %s", value.String())
	}
}

func sliceValue(value Value, start, end int64) (Value, error) {
	if start < 0 || end < start {
		return VoidValue(), fmt.Errorf("invalid slice [%d:%d]", start, end)
	}
	switch value.kind {
	case valueList:
		if int(end) > len(value.listVal) {
			return VoidValue(), fmt.Errorf("slice end %d out of range", end)
		}
		return ListValue(value.listVal[start:end]), nil
	case valueString:
		if int(end) > len(value.strVal) {
			return VoidValue(), fmt.Errorf("slice end %d out of range", end)
		}
		return StringValue(value.strVal[start:end]), nil
	default:
		return VoidValue(), fmt.Errorf("slicing requires list or string, got %s", value.String())
	}
}

func valuesEqual(left, right Value) bool {
	if left.kind == valueNull || right.kind == valueNull {
		return left.kind == right.kind
	}
	if left.kind == valueFloat || right.kind == valueFloat {
		lf, err := numericAsFloat(left)
		if err != nil {
			return false
		}
		rf, err := numericAsFloat(right)
		if err != nil {
			return false
		}
		return lf == rf
	}
	if left.kind != right.kind {
		return false
	}
	switch left.kind {
	case valueVoid:
		return true
	case valueInt:
		return left.int64Val == right.int64Val
	case valueBool:
		return left.boolVal == right.boolVal
	case valueString:
		return left.strVal == right.strVal
	case valueList:
		if len(left.listVal) != len(right.listVal) {
			return false
		}
		for i := range left.listVal {
			if !valuesEqual(left.listVal[i], right.listVal[i]) {
				return false
			}
		}
		return true
	case valueStruct:
		if left.structVal == nil || right.structVal == nil || left.structVal.Name != right.structVal.Name {
			return false
		}
		if len(left.structVal.Fields) != len(right.structVal.Fields) {
			return false
		}
		for name, value := range left.structVal.Fields {
			if !valuesEqual(value, right.structVal.Fields[name]) {
				return false
			}
		}
		return true
	case valueFunction:
		return left.funcName == right.funcName
	default:
		return false
	}
}

func annotateRuntimeError(pos lexer.Pos, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.HasPrefix(message, pos.String()+":") {
		return err
	}
	return fmt.Errorf("%s: %w", pos, err)
}

func isVoidTypeExpr(typ ast.TypeExpr) bool {
	if typ == nil {
		return true
	}
	named, ok := typ.(*ast.NamedType)
	return ok && named.Name == "void"
}
