package semantic

import (
	"elisacore/src/ast"
	"sort"
)

func CloneConstValue(value ConstValue) ConstValue {
	cloned := value
	if len(value.Elems) != 0 {
		cloned.Elems = make([]ConstValue, len(value.Elems))
		for i, elem := range value.Elems {
			cloned.Elems[i] = CloneConstValue(elem)
		}
	}
	if len(value.Fields) != 0 {
		cloned.Fields = make(map[string]ConstValue, len(value.Fields))
		for name, fieldValue := range value.Fields {
			cloned.Fields[name] = CloneConstValue(fieldValue)
		}
	}
	if len(value.Dict) != 0 {
		cloned.Dict = make([]ConstDictEntry, len(value.Dict))
		for i, entry := range value.Dict {
			cloned.Dict[i] = ConstDictEntry{Key: CloneConstValue(entry.Key), Value: CloneConstValue(entry.Value)}
		}
	}
	if value.Value != nil {
		child := CloneConstValue(*value.Value)
		cloned.Value = &child
	}
	return cloned
}

func ConstValueStaticType(namedTypes map[string]Type, value ConstValue) Type {
	if namedTypes == nil {
		return &ConstValueType{Value: value}
	}
	switch value.Kind {
	case ConstInt:
		if t := namedTypes["i64"]; t != nil {
			return t
		}
		if t := namedTypes["int"]; t != nil {
			return t
		}
	case ConstFloat:
		if t := namedTypes["f64"]; t != nil {
			return t
		}
	case ConstBool:
		if t := namedTypes["bool"]; t != nil {
			return t
		}
	case ConstString:
		if t := namedTypes["string"]; t != nil {
			return t
		}
		if t := namedTypes["u8"]; t != nil {
			return &RefType{Elem: t, State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
		}
	case ConstOptional:
		if value.Some && value.Value != nil {
			return &OptionalType{Value: ConstValueStaticType(namedTypes, *value.Value)}
		}
	}
	return &ConstValueType{Value: value}
}

func ConstReflectionCallValue(name string, args []ast.Expr, lookupType func(string) (Type, bool)) (ConstValue, bool) {
	if lookupType == nil || len(args) != 1 {
		return ConstValue{}, false
	}
	typeName, ok := QualifiedNameExpr(args[0])
	if !ok {
		return ConstValue{}, false
	}
	t, ok := lookupType(typeName)
	if !ok || t == nil {
		return ConstValue{}, false
	}
	switch name {
	case "variants":
		return ConstReflectionVariants(t)
	case "fields":
		return ConstReflectionFields(t)
	default:
		return ConstValue{}, false
	}
}

func (a *Analyzer) analyzeConstReflectionCall(expr *ast.CallExpr) (Type, bool) {
	if expr == nil {
		return nil, false
	}
	if fieldExpr, ok := expr.Func.(*ast.FieldExpr); ok && fieldExpr != nil && fieldExpr.Field == "has_field" {
		objectType := a.analyzeExpr(fieldExpr.Object)
		if _, ok := objectType.(*ConstValueType); !ok {
			return nil, false
		}
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		if len(expr.Args) != 1 || expr.NamedArgCount() != 0 {
			a.errorf(expr.Pos(), "has_field expects one string argument")
		}
		return a.namedTypes["bool"], true
	}
	name, _ := QualifiedNameExpr(expr.Func)
	if name != "variants" && name != "fields" {
		return nil, false
	}
	value, ok := ConstReflectionCallValue(name, expr.Args, func(typeName string) (Type, bool) {
		t, _, ok := a.lookupVisibleType(typeName)
		return t, ok
	})
	if !ok {
		if len(expr.Args) == 1 {
			if typeName, nameOK := QualifiedNameExpr(expr.Args[0]); nameOK {
				a.errorf(expr.Args[0].Pos(), "%s expects a reflected enum, tree category, or struct type, got %q", name, typeName)
			} else {
				a.errorf(expr.Args[0].Pos(), "%s expects a type name", name)
			}
		} else {
			a.errorf(expr.Pos(), "%s expects exactly one type argument", name)
		}
		return invalidType, true
	}
	return &ConstValueType{Value: value}, true
}

func QualifiedNameExpr(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "" {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := QualifiedNameExpr(n.Object)
		if !ok || n.Field == "" {
			return "", false
		}
		return base + "." + n.Field, true
	case *ast.ParenExpr:
		return QualifiedNameExpr(n.Inner)
	default:
		return "", false
	}
}

func ConstReflectionVariants(t Type) (ConstValue, bool) {
	switch tt := StripAggregateStateType(t).(type) {
	case *ConstEnumType:
		elems := make([]ConstValue, 0, len(tt.Members))
		for i, member := range tt.Members {
			elems = append(elems, constReflectionVariantRecord(member.Name, int64(i), member.Value, nil, nil))
		}
		return ConstValue{Kind: ConstList, Elems: elems}, true
	case *EnumType:
		elems := make([]ConstValue, 0, len(tt.Variants))
		for i, variant := range tt.Variants {
			elems = append(elems, constReflectionVariantRecord(variant.Name, int64(i), int64(variant.Tag), variant.PayloadNames, variant.Payload))
		}
		return ConstValue{Kind: ConstList, Elems: elems}, true
	default:
		return ConstValue{}, false
	}
}

func ConstReflectionFields(t Type) (ConstValue, bool) {
	switch tt := StripAggregateStateType(t).(type) {
	case *StructType:
		return ConstValue{Kind: ConstList, Elems: constReflectionFieldsFromMap(tt.Fields, structFieldOrder(tt))}, true
	case *EnumType:
		return ConstValue{Kind: ConstList, Elems: constReflectionFieldsFromMap(tt.Common, sortedFieldNames(tt.Common))}, true
	default:
		return ConstValue{}, false
	}
}

func ConstReflectionRecordField(value ConstValue, field string) (ConstValue, bool) {
	if value.Kind != ConstRecord || value.Fields == nil {
		return ConstValue{}, false
	}
	fieldValue, ok := value.Fields[field]
	return fieldValue, ok
}

func ConstReflectionRecordHasField(value ConstValue, fieldName string) (ConstValue, bool) {
	fieldsValue, ok := ConstReflectionRecordField(value, "fields")
	if !ok || fieldsValue.Kind != ConstList {
		return ConstValue{}, false
	}
	for _, field := range fieldsValue.Elems {
		nameValue, ok := ConstReflectionRecordField(field, "name")
		if ok && nameValue.Kind == ConstString && nameValue.String == fieldName {
			return ConstValue{Kind: ConstBool, Bool: true}, true
		}
	}
	return ConstValue{Kind: ConstBool, Bool: false}, true
}

func constReflectionVariantRecord(name string, index int64, tag int64, payloadNames []string, payloadTypes []Type) ConstValue {
	fields := make([]ConstValue, 0, len(payloadTypes))
	for i := range payloadTypes {
		fieldName := ""
		if i < len(payloadNames) {
			fieldName = payloadNames[i]
		}
		fields = append(fields, constReflectionFieldRecord(fieldName, int64(i), false))
	}
	return ConstValue{Kind: ConstRecord, Fields: map[string]ConstValue{
		"name":        {Kind: ConstString, String: name},
		"index":       {Kind: ConstInt, Int: index},
		"tag":         {Kind: ConstInt, Int: tag},
		"field_count": {Kind: ConstInt, Int: int64(len(payloadTypes))},
		"fields":      {Kind: ConstList, Elems: fields},
	}}
}

func constReflectionFieldRecord(name string, index int64, mutable bool) ConstValue {
	return ConstValue{Kind: ConstRecord, Fields: map[string]ConstValue{
		"name":    {Kind: ConstString, String: name},
		"index":   {Kind: ConstInt, Int: index},
		"mutable": {Kind: ConstBool, Bool: mutable},
	}}
}

func constReflectionFieldsFromMap(fields map[string]Field, order []string) []ConstValue {
	if len(fields) == 0 {
		return nil
	}
	if len(order) == 0 {
		order = sortedFieldNames(fields)
	}
	elems := make([]ConstValue, 0, len(order))
	for _, name := range order {
		field, ok := fields[name]
		if !ok {
			continue
		}
		elems = append(elems, constReflectionFieldRecord(field.Name, int64(len(elems)), field.Mutable))
	}
	return elems
}

func sortedFieldNames(fields map[string]Field) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func structFieldOrder(t *StructType) []string {
	if t == nil || t.Decl == nil {
		return nil
	}
	names := make([]string, 0, len(t.Decl.Fields))
	for _, field := range t.Decl.Fields {
		if field.BitGroup != nil {
			for _, member := range field.BitGroup.Members {
				names = append(names, member.Name)
			}
			continue
		}
		names = append(names, field.Name)
	}
	return names
}

func astFieldDeclNames(fields []ast.FieldDecl) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		if field.BitGroup != nil {
			for _, member := range field.BitGroup.Members {
				names = append(names, member.Name)
			}
			continue
		}
		names = append(names, field.Name)
	}
	return names
}
