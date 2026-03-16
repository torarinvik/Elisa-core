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
	case *semantic.GenericInstanceType:
		base, ok := tt.Base.(*semantic.StructType)
		if !ok || base.Decl == nil {
			return nil, fmt.Errorf("generic struct literal requires a struct-backed concrete type")
		}
		if len(base.TypeParams) != len(tt.Args) {
			return nil, fmt.Errorf("generic struct %s has %d args, expected %d", base.Name, len(tt.Args), len(base.TypeParams))
		}
		subst := make(map[string]semantic.Type, len(base.TypeParams))
		for i, name := range base.TypeParams {
			subst[name] = tt.Args[i]
		}
		fields := make([]structLiteralField, 0, len(base.Decl.Fields))
		for i, fieldDecl := range base.Decl.Fields {
			field, ok := base.Fields[fieldDecl.Name]
			if !ok {
				return nil, fmt.Errorf("missing semantic field %s.%s", base.Name, fieldDecl.Name)
			}
			fields = append(fields, structLiteralField{Decl: fieldDecl, Type: substituteType(field.Type, subst), Index: i})
		}
		return fields, nil
	default:
		return nil, fmt.Errorf("struct literal requires a concrete struct type, got %s", t.String())
	}
}
