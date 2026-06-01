package interpreter

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const maxLoopIterations = 1_000_000

type Options struct {
	Entry    string
	Stdout   io.Writer
	Debugger *Debugger
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
	valueRef
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
	lambdaVal *lambdaValue
	refSlot   *valueSlot
}
type StructValue struct {
	Name       string
	FieldOrder []string
	Fields     map[string]Value
}
type lambdaValue struct {
	id       int
	expr     *ast.LambdaExpr
	captures map[string]Value
}
type runtimeFunc func([]Value) (Value, error)
type runtimeFuncInfo struct {
	fn     runtimeFunc
	params []ast.ParamDecl
}
type Interpreter struct {
	result       *semantic.Result
	stdout       io.Writer
	functions    map[string]*ast.FuncDecl
	structs      map[string]*ast.StructDecl
	consts       map[string]Value
	globals      map[string]Value
	runtimeFuncs map[string]runtimeFuncInfo
	nextLambdaID int
	debugger     *Debugger
}
type frame struct {
	locals    map[string]Value
	parent    *frame
	namespace string
	function  string
}

func qualifiedInterpreterName(namespace string, name string) string {
	name = strings.TrimSpace(name)
	namespace = strings.TrimSpace(namespace)
	if name == "" || strings.Contains(name, ".") {
		return name
	}
	if namespace == "" {
		return name
	}
	return namespace + "." + name
}

func interpreterNamespaceFromName(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx]
	}
	return ""
}

func interpreterVisibleNames(frame *frame, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if strings.Contains(name, ".") {
		return []string{name}
	}
	for current := frame; current != nil; current = current.parent {
		if current.namespace != "" {
			return []string{current.namespace + "." + name, name}
		}
	}
	return []string{name}
}

func interpreterQualifiedFieldName(expr *ast.FieldExpr) (string, bool) {
	if expr == nil || expr.Safe {
		return "", false
	}
	parts := []string{expr.Field}
	for current := expr.Object; current != nil; {
		switch n := current.(type) {
		case *ast.Ident:
			parts = append(parts, n.Name)
			for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
				parts[left], parts[right] = parts[right], parts[left]
			}
			return strings.Join(parts, "."), true
		case *ast.FieldExpr:
			if n.Safe {
				return "", false
			}
			parts = append(parts, n.Field)
			current = n.Object
		default:
			return "", false
		}
	}
	return "", false
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
		debugger:  options.Debugger,
	}
	if interp.debugger != nil {
		interp.debugger.Reset()
	}
	interp.runtimeFuncs = map[string]runtimeFuncInfo{
		"puts": {
			fn:     interp.runtimePuts,
			params: []ast.ParamDecl{{Name: "text"}},
		},
		"rt_puts": {
			fn:     interp.runtimePuts,
			params: []ast.ParamDecl{{Name: "text"}},
		},
		"assert": {
			fn:     interp.runtimeAssert,
			params: []ast.ParamDecl{{Name: "cond"}},
		},
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
	if interp.debugger != nil {
		if condErr := interp.debugger.deferredConditionError(); condErr != nil {
			return nil, condErr
		}
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
		if v.lambdaVal != nil {
			return "<func lambda>"
		}
		return "<func " + v.funcName + ">"
	case valueRef:
		if v.refSlot == nil {
			return "<ref nil>"
		}
		return "<ref " + v.refSlot.get().String() + ">"
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
	case valueFunction:
		if v.lambdaVal != nil {
			captures := make(map[string]Value, len(v.lambdaVal.captures))
			for name, value := range v.lambdaVal.captures {
				captures[name] = value.Clone()
			}
			cloned.lambdaVal = &lambdaValue{id: v.lambdaVal.id, expr: v.lambdaVal.expr, captures: captures}
		}
	case valueRef:
		cloned.refSlot = v.refSlot
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
func RefValue(slot *valueSlot) Value  { return Value{kind: valueRef, refSlot: slot} }
func LambdaFunctionValue(id int, expr *ast.LambdaExpr, captures map[string]Value) Value {
	cloned := make(map[string]Value, len(captures))
	for name, value := range captures {
		cloned[name] = value.Clone()
	}
	return Value{kind: valueFunction, lambdaVal: &lambdaValue{id: id, expr: expr, captures: cloned}}
}
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
func derefValue(value Value) Value {
	if value.kind == valueRef && value.refSlot != nil {
		return value.refSlot.get()
	}
	return value
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
	for _, decl := range i.result.ActiveFile().Decls {
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
	return i.initializeGlobalsFromDeclInNamespace(decl, "")
}

func (i *Interpreter) initializeGlobalsFromDeclInNamespace(decl ast.Decl, namespace string) error {
	switch n := decl.(type) {
	case *ast.GlobalDecl:
		initFrame := &frame{locals: map[string]Value{}, namespace: namespace}
		value, err := i.evaluateInitializer(initFrame, n.Type, n.Value)
		if err != nil {
			return err
		}
		i.globals[qualifiedInterpreterName(namespace, n.Name)] = value
	case *ast.NamespaceDecl:
		childNamespace := qualifiedInterpreterName(namespace, n.Name)
		for _, nested := range n.Decls {
			if err := i.initializeGlobalsFromDeclInNamespace(nested, childNamespace); err != nil {
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
	value = derefValue(value)
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
		return runtimeFn.fn(cloneArgs(args))
	}
	fn, ok := i.functions[name]
	if !ok || fn == nil {
		return VoidValue(), fmt.Errorf("interpreter does not know function %q", name)
	}
	return i.callFunction(name, fn, args, nil)
}
func cloneArgs(args []Value) []Value {
	cloned := make([]Value, len(args))
	for i, arg := range args {
		cloned[i] = arg.Clone()
	}
	return cloned
}
func (i *Interpreter) loweredFuncParams(name string, fn *ast.FuncDecl) []ast.ParamDecl {
	params := append([]ast.ParamDecl(nil), fn.Params...)
	if i == nil || i.result == nil || i.result.GlobalScope == nil {
		return params
	}
	if sym, ok := i.result.GlobalScope.Lookup(name); ok && sym != nil {
		if fnType, ok := sym.Type.(*semantic.FuncType); ok {
			for _, name := range fnType.ImplicitParamNames {
				params = append(params, ast.ParamDecl{Name: name})
			}
		}
	}
	return params
}
func (i *Interpreter) callFunction(name string, fn *ast.FuncDecl, positional []Value, named map[string]Value) (Value, error) {
	frame := &frame{locals: map[string]Value{}, namespace: interpreterNamespaceFromName(name), function: name}
	if err := bindCallArgs(frame.locals, i.loweredFuncParams(name, fn), positional, named); err != nil {
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
func childFrame(parent *frame) *frame {
	return &frame{locals: map[string]Value{}, parent: parent, namespace: parent.namespace, function: parent.function}
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
