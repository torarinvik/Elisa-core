package interpreter

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"io"
	"maps"
	"slices"
)

func (i *Interpreter) bootstrap() error {
	if i.result.GlobalScope != nil {
		for name, sym := range i.result.GlobalScope.Symbols {
			if sym == nil {
				continue
			}
			switch sym.Kind {
			case semantic.SymbolFunc:
				if decl, ok := sym.Node.(*ast.FuncDecl); ok && decl != nil {
					if decl.IsGhost {
						continue
					}
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
	if mockFn, ok := i.mocks[name]; ok {
		return mockFn(cloneArgs(args))
	}
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
	if err := bindCallArgs(frame.locals, i.loweredFuncParams(name, fn), positional, named, func(e ast.Expr) (Value, error) {
		return i.evalExpr(frame, e)
	}); err != nil {
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
func bindCallArgs(dst map[string]Value, params []ast.ParamDecl, positional []Value, named map[string]Value, evalDefault func(ast.Expr) (Value, error)) error {
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
	for _, name := range slices.Sorted(maps.Keys(named)) { // map: sort so the reported argument is stable
		value := named[name]
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
			// A parameter carrying a DEFAULT is not missing — it is omitted. Only the
			// positional path filled defaults (its caller appends them), so a NAMED call that
			// skipped a defaulted parameter errored here, and `-emit interpret` answered 111
			// for test/repro/generic_default_argument.elisa where the fixture itself records
			// 127. `named[i64](0, c: 9)` never bound `b`, whose default is 2.
			if param.DefaultValue != nil && evalDefault != nil {
				value, err := evalDefault(param.DefaultValue)
				if err != nil {
					return err
				}
				dst[param.Name] = value.Clone()
				continue
			}
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
