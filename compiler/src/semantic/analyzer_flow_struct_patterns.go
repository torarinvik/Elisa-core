package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

type moveBindResolvedField struct {
	Name    string
	Type    Type
	Mutable bool
	Index   int
}

type moveBindResolvedVariantField struct {
	Path     []string
	Type     Type
	BindName string
	Position lexer.Pos
}

func (a *Analyzer) resolvedStructFields(actual Type) ([]moveBindResolvedField, bool) {
	var (
		base     *StructType
		bindings map[string]Type
	)
	actual = StripAggregateStateType(actual)
	switch tt := actual.(type) {
	case *StructType:
		base = tt
	case *TreeVariantViewType:
		if tt == nil {
			return nil, false
		}
		fieldDecls := treeExactMemberFieldDecls(tt)
		fields := make([]moveBindResolvedField, 0, len(fieldDecls))
		for i, fieldDecl := range fieldDecls {
			resolved, ok := TreeExactFieldInfo(tt, fieldDecl.Name)
			if !ok {
				continue
			}
			fields = append(fields, moveBindResolvedField{Name: fieldDecl.Name, Type: resolved.Type, Mutable: resolved.Mutable, Index: i})
		}
		return fields, true
	case *TreeBlockType:
		if tt == nil {
			return nil, false
		}
		fieldDecls := treeExactMemberFieldDecls(tt)
		fields := make([]moveBindResolvedField, 0, len(fieldDecls))
		for i, fieldDecl := range fieldDecls {
			resolved, ok := TreeExactFieldInfo(tt, fieldDecl.Name)
			if !ok {
				continue
			}
			fields = append(fields, moveBindResolvedField{Name: fieldDecl.Name, Type: resolved.Type, Mutable: resolved.Mutable, Index: i})
		}
		return fields, true
	case *TreeStructType:
		if tt == nil {
			return nil, false
		}
		fieldDecls := treeExactMemberFieldDecls(tt)
		fields := make([]moveBindResolvedField, 0, len(fieldDecls))
		for i, fieldDecl := range fieldDecls {
			resolved, ok := TreeExactFieldInfo(tt, fieldDecl.Name)
			if !ok {
				continue
			}
			fields = append(fields, moveBindResolvedField{Name: fieldDecl.Name, Type: resolved.Type, Mutable: resolved.Mutable, Index: i})
		}
		return fields, true
	case *TupleType:
		if tt == nil {
			return nil, false
		}
		fields := make([]moveBindResolvedField, 0, len(tt.Fields))
		for i, field := range tt.Fields {
			fields = append(fields, moveBindResolvedField{Name: field.Name, Type: field.Type, Mutable: false, Index: i})
		}
		return fields, true
	case *StoreRowViewType:
		if tt == nil || tt.Store == nil || tt.Store.Decl == nil {
			return nil, false
		}
		fields := make([]moveBindResolvedField, 0, len(tt.Store.Decl.Fields))
		for _, fieldDecl := range tt.Store.Decl.Fields {
			field, ok := storeRowViewField(tt, fieldDecl.Name)
			if !ok {
				continue
			}
			fields = append(fields, moveBindResolvedField{Name: fieldDecl.Name, Type: field.Type, Mutable: field.Mutable})
		}
		return fields, true
	case *GenericInstanceType:
		structBase, ok := tt.Base.(*StructType)
		if !ok {
			return nil, false
		}
		base = structBase
		bindings = genericBindingsForStructInstance(base, tt.Args)
	default:
		return nil, false
	}
	if base == nil {
		return nil, false
	}
	if base.Decl == nil {
		return nil, false
	}
	fields := make([]moveBindResolvedField, 0, len(base.Decl.Fields))
	for i := 0; i < len(base.Decl.Fields); i++ {
		fieldDecl := base.Decl.Fields[i]
		field, ok := base.Fields[fieldDecl.Name]
		if !ok {
			continue
		}
		fieldType := field.Type
		if len(bindings) != 0 {
			fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
		}
		fields = append(fields, moveBindResolvedField{Name: fieldDecl.Name, Type: fieldType, Mutable: field.Mutable, Index: i})
	}
	return fields, true
}

func (a *Analyzer) resolveMoveBindStructPattern(pattern *ast.MoveBindStructPattern, actual Type) ([]moveBindResolvedField, bool) {
	if pattern == nil {
		return nil, false
	}
	actual = StripAggregateStateType(actual)
	fields, ok := a.resolvedStructFields(actual)
	if !ok {
		if pattern.TypeName == "" {
			a.errorf(pattern.Pos(), "destructuring pattern requires a concrete struct or store-row value, got %s", actual)
		} else {
			a.errorf(pattern.Pos(), "move-as pattern %q requires a concrete struct value, got %s", pattern.TypeName, actual)
		}
		return nil, false
	}
	switch tt := actual.(type) {
	case *StructType:
		if pattern.TypeName != "" && tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "move-as pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, false
		}
		if pattern.TypeName != "" && tt.Decl == nil {
			a.errorf(pattern.Pos(), "move-as destructuring is not supported for builtin struct %q", tt.Name)
			return nil, false
		}
	case *GenericInstanceType:
		base, _ := tt.Base.(*StructType)
		if pattern.TypeName != "" && (base == nil || base.Name != pattern.TypeName) {
			got := actual.String()
			if base != nil {
				got = base.Name
			}
			a.errorf(pattern.Pos(), "move-as pattern expects struct %q, got %q", pattern.TypeName, got)
			return nil, false
		}
		if pattern.TypeName != "" && base != nil && base.Decl == nil {
			a.errorf(pattern.Pos(), "move-as destructuring is not supported for builtin struct %q", base.Name)
			return nil, false
		}
	case *TreeBlockType:
		if pattern.TypeName != "" && tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "destructuring pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, false
		}
	case *TreeStructType:
		if pattern.TypeName != "" && tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "destructuring pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, false
		}
	case *TupleType:
		if pattern.TypeName == "" {
			a.errorf(pattern.Pos(), "destructuring pattern requires a concrete struct or store-row value, got %s", actual)
		} else {
			a.errorf(pattern.Pos(), "move-as pattern %q requires a concrete struct value, got %s", pattern.TypeName, actual)
		}
		return nil, false
	}
	if pattern.Brace {
		resolved := make([]moveBindResolvedField, len(pattern.Args))
		fieldIndexes := make(map[string]moveBindResolvedField, len(fields))
		for _, field := range fields {
			fieldIndexes[field.Name] = field
		}
		seen := map[string]lexer.Pos{}
		ok := true
		for i, arg := range pattern.Args {
			fieldName := arg.Field
			if fieldName == "" {
				fieldName = arg.Name
			}
			field, exists := fieldIndexes[fieldName]
			if !exists {
				typeName := pattern.TypeName
				if typeName == "" {
					typeName = actual.String()
				}
				a.errorf(arg.Position, "struct %q has no field %q", typeName, fieldName)
				ok = false
				continue
			}
			if prev, exists := seen[fieldName]; exists {
				a.errorf(arg.Position, "struct field %q is bound more than once (first at %s:%d:%d)", fieldName, prev.File, prev.Line, prev.Col)
				ok = false
				continue
			}
			seen[fieldName] = arg.Position
			resolved[i] = field
		}
		return resolved, ok
	}
	if len(pattern.Args) != len(fields) {
		a.errorf(pattern.Pos(), "move-as pattern %q expects %d bindings, got %d", pattern.TypeName, len(fields), len(pattern.Args))
	}
	limit := len(pattern.Args)
	if len(fields) < limit {
		limit = len(fields)
	}
	return fields[:limit], true
}

func (a *Analyzer) resolveMatchStructPattern(pattern *ast.MatchStructPattern, actual Type) ([]moveBindResolvedField, []*ast.MatchPatternArg, bool) {
	if pattern == nil {
		return nil, nil, false
	}
	actual = StripAggregateStateType(actual)
	fields, ok := a.resolvedStructFields(actual)
	if !ok {
		a.errorf(pattern.Pos(), "struct pattern %q requires a concrete struct value, got %s", pattern.TypeName, actual)
		return nil, nil, false
	}
	switch tt := actual.(type) {
	case *StructType:
		if tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "struct pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, nil, false
		}
		if tt.Decl == nil {
			a.errorf(pattern.Pos(), "struct pattern destructuring is not supported for builtin struct %q", tt.Name)
			return nil, nil, false
		}
	case *GenericInstanceType:
		base, _ := tt.Base.(*StructType)
		if base == nil || base.Name != pattern.TypeName {
			got := actual.String()
			if base != nil {
				got = base.Name
			}
			a.errorf(pattern.Pos(), "struct pattern expects struct %q, got %q", pattern.TypeName, got)
			return nil, nil, false
		}
		if base.Decl == nil {
			a.errorf(pattern.Pos(), "struct pattern destructuring is not supported for builtin struct %q", base.Name)
			return nil, nil, false
		}
	case *TreeBlockType:
		if tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "struct pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, nil, false
		}
	case *TreeStructType:
		if tt.Name != pattern.TypeName {
			a.errorf(pattern.Pos(), "struct pattern expects struct %q, got %q", pattern.TypeName, tt.Name)
			return nil, nil, false
		}
	case *TupleType:
		a.errorf(pattern.Pos(), "struct pattern %q requires a concrete struct value, got %s", pattern.TypeName, actual)
		return nil, nil, false
	}
	ordered := make([]*ast.MatchPatternArg, len(fields))
	fieldIndexes := make(map[string]int, len(fields))
	for i := range fields {
		fieldIndexes[fields[i].Name] = i
	}
	seen := map[int]lexer.Pos{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		if arg.Name == "" {
			a.errorf(arg.Position, "struct pattern fields must use named field matches")
			continue
		}
		index, ok := fieldIndexes[arg.Name]
		if !ok {
			a.errorf(arg.Position, "struct %q has no field %q", pattern.TypeName, arg.Name)
			continue
		}
		if prev, exists := seen[index]; exists {
			a.errorf(arg.Position, "struct %q field %q is matched more than once (first at %s:%d:%d)", pattern.TypeName, arg.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[index] = arg.Position
		ordered[index] = arg
	}
	pattern.ResolvedArgs = ordered
	return fields, ordered, true
}

func (a *Analyzer) resolveMatchTuplePattern(pattern *ast.MatchTuplePattern, actual Type) ([]moveBindResolvedField, bool) {
	if pattern == nil {
		return nil, false
	}
	actual = StripAggregateStateType(actual)
	tupleType, ok := actual.(*TupleType)
	if !ok || tupleType == nil {
		a.errorf(pattern.Pos(), "tuple pattern requires a tuple value, got %s", actual)
		return nil, false
	}
	fields, ok := a.resolvedStructFields(actual)
	if !ok {
		a.errorf(pattern.Pos(), "tuple pattern requires a tuple value, got %s", actual)
		return nil, false
	}
	if len(pattern.Elems) != len(fields) {
		a.errorf(pattern.Pos(), "tuple pattern expects %d elements, got %d", len(fields), len(pattern.Elems))
	}
	return fields, true
}
