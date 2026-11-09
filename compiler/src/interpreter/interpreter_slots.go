package interpreter

import (
	"elisacore/src/ast"
	"fmt"
	"maps"
	"slices"
)

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
	var search func([]ast.Decl, string) (*ast.StructDecl, bool)
	search = func(decls []ast.Decl, namespace string) (*ast.StructDecl, bool) {
		for _, decl := range decls {
			switch n := decl.(type) {
			case *ast.StructDecl:
				if n != nil && (n.Name == name || qualifiedInterpreterName(namespace, n.Name) == name) {
					return n, true
				}
			case *ast.NamespaceDecl:
				if found, ok := search(n.Decls, qualifiedInterpreterName(namespace, n.Name)); ok {
					return found, true
				}
			}
		}
		return nil, false
	}
	decl, ok := search(activeFile.Decls, "")
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
	for _, name := range slices.Sorted(maps.Keys(named)) { // map: sort so the reported field is stable
		value := named[name]
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
	for _, candidate := range interpreterVisibleNames(frame, name) {
		if value, ok := i.globals[candidate]; ok {
			return value.Clone(), nil
		}
		if value, ok := i.consts[candidate]; ok {
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
	if name == "perm" {
		// The perm program-lifetime region: the interpreter has no arenas (values are
		// GC-managed), so a threaded region argument is satisfied by a zero Arena.
		return StructInstanceValue("Arena", []string{"begin", "end", "end_index", "strategy"}, map[string]Value{
			"begin":     NullValue(),
			"end":       NullValue(),
			"end_index": IntValue(0),
			"strategy":  IntValue(0),
		}), nil
	}
	return VoidValue(), fmt.Errorf("undefined name %q", name)
}
func (i *Interpreter) resolveSlot(frame *frame, expr ast.Expr) (*valueSlot, error) {
	switch n := expr.(type) {
	case *ast.Ident:
		name := n.Name
		for current := frame; current != nil; current = current.parent {
			if value, ok := current.locals[name]; ok {
				if value.kind == valueRef && value.refSlot != nil {
					return value.refSlot, nil
				}
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
		for _, candidate := range interpreterVisibleNames(frame, name) {
			if _, ok := i.globals[candidate]; ok {
				globalName := candidate
				return &valueSlot{
					get: func() Value { return i.globals[globalName].Clone() },
					set: func(value Value) error {
						i.globals[globalName] = value.Clone()
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
		if name, ok := interpreterQualifiedFieldName(n); ok {
			for _, candidate := range interpreterVisibleNames(frame, name) {
				if _, ok := i.globals[candidate]; ok {
					globalName := candidate
					return &valueSlot{
						get: func() Value { return i.globals[globalName].Clone() },
						set: func(value Value) error {
							i.globals[globalName] = value.Clone()
							return nil
						},
					}, nil
				}
			}
		}
		parent, err := i.resolveSlot(frame, n.Object)
		if err != nil {
			return nil, err
		}
		return &valueSlot{
			get: func() Value {
				parentValue := derefValue(parent.get())
				if parentValue.kind != valueStruct || parentValue.structVal == nil {
					return VoidValue()
				}
				return parentValue.structVal.Fields[n.Field].Clone()
			},
			set: func(value Value) error {
				parentValue := derefValue(parent.get())
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
		if derefValue(parent.get()).kind == valueDict {
			key := indexValue
			return &valueSlot{
				get: func() Value {
					return dictLookup(derefValue(parent.get()), key)
				},
				set: func(value Value) error {
					return parent.set(dictStore(derefValue(parent.get()), key, value))
				},
			}, nil
		}
		index, err := requireInt(indexValue)
		if err != nil {
			return nil, err
		}
		return &valueSlot{
			get: func() Value {
				value, _ := indexValueAt(derefValue(parent.get()), index)
				return value
			},
			set: func(value Value) error {
				parentValue := derefValue(parent.get())
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
