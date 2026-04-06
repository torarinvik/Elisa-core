//go:build cgo

package backend

import (
	"fmt"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

type structLiteralField struct {
	Decl  ast.FieldDecl
	Type  semantic.Type
	Index int
}

func (g *llvmGenerator) structLiteralFields(t semantic.Type) ([]structLiteralField, error) {
	t = semantic.StripAggregateStateType(t)
	switch tt := t.(type) {
	case *semantic.StructType:
		if tt.Decl == nil {
			return nil, fmt.Errorf("struct %s is missing declaration metadata", tt.Name)
		}
		fields := make([]structLiteralField, 0, len(tt.Decl.Fields))
		for i, fieldDecl := range tt.Decl.Fields {
			field, ok := tt.Fields[fieldDecl.Name]
			if !ok {
				return nil, fmt.Errorf("missing semantic field %s.%s", tt.Name, fieldDecl.Name)
			}
			fields = append(fields, structLiteralField{Decl: fieldDecl, Type: field.Type, Index: i})
		}
		return fields, nil
	case *semantic.TupleType:
		fields := make([]structLiteralField, 0, len(tt.Fields))
		for i, field := range tt.Fields {
			fields = append(fields, structLiteralField{Decl: ast.FieldDecl{Name: field.Name}, Type: field.Type, Index: i})
		}
		return fields, nil
	case *semantic.GenericInstanceType:
		base, ok := tt.Base.(*semantic.StructType)
		if !ok || base.Decl == nil {
			return nil, fmt.Errorf("generic struct literal requires a struct-backed concrete type")
		}
		params := structGenericParams(base)
		if len(params) != len(tt.Args) {
			return nil, fmt.Errorf("generic struct %s has %d args, expected %d", base.Name, len(tt.Args), len(params))
		}
		subst := genericBindingsForArgs(params, tt.Args)
		fields := make([]structLiteralField, 0, len(base.Decl.Fields))
		for i, fieldDecl := range base.Decl.Fields {
			field, ok := base.Fields[fieldDecl.Name]
			if !ok {
				return nil, fmt.Errorf("missing semantic field %s.%s", base.Name, fieldDecl.Name)
			}
			fields = append(fields, structLiteralField{Decl: fieldDecl, Type: substituteType(field.Type, subst, g.result.StaticImpls), Index: i})
		}
		return fields, nil
	case *semantic.TreeBlockType:
		if tt == nil || tt.Decl == nil {
			return nil, fmt.Errorf("tree block literal requires declaration metadata")
		}
		fields := make([]structLiteralField, 0, len(tt.Decl.Fields))
		for i, fieldDecl := range tt.Decl.Fields {
			field, ok := tt.Fields[fieldDecl.Name]
			if !ok {
				return nil, fmt.Errorf("missing semantic field %s.%s", tt.Name, fieldDecl.Name)
			}
			fields = append(fields, structLiteralField{Decl: fieldDecl, Type: field.Type, Index: i})
		}
		return fields, nil
	case *semantic.TreeStructType:
		if tt == nil || tt.Decl == nil {
			return nil, fmt.Errorf("tree struct literal requires declaration metadata")
		}
		fields := make([]structLiteralField, 0, len(tt.Decl.Fields))
		for i, fieldDecl := range tt.Decl.Fields {
			field, ok := tt.Fields[fieldDecl.Name]
			if !ok {
				return nil, fmt.Errorf("missing semantic field %s.%s", tt.Name, fieldDecl.Name)
			}
			fields = append(fields, structLiteralField{Decl: fieldDecl, Type: field.Type, Index: i})
		}
		return fields, nil
	default:
		return nil, fmt.Errorf("struct literal requires a concrete struct type, got %s", t.String())
	}
}
