package predicatetypes

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

func ParameterSyntax(params []ast.ParamDecl, parameterSchemaValid bool, parameterLimit, byteBudget int) bool {
	if len(params) == 0 {
		return true
	}
	if !parameterSchemaValid || len(params) > parameterLimit {
		return false
	}
	names := map[string]bool{}
	total := 0
	for _, param := range params {
		total += len(param.Name)
		if param.Name == "" || total > byteBudget || names[param.Name] || param.Mutable || param.DefaultValue != nil {
			return false
		}
		names[param.Name] = true
		switch typ := param.Type.(type) {
		case *ast.NamedType:
			if typ == nil {
				return false
			}
			copy := *typ
			copy.Position = lexer.Pos{}
			copy.Name = ""
			if (typ.Name != "i64" && typ.Name != "int") || !reflect.ValueOf(copy).IsZero() {
				return false
			}
		case *ast.BuiltinTypeExpr:
			if typ == nil {
				return false
			}
			copy := *typ
			copy.Position = lexer.Pos{}
			copy.Name = ""
			if len(copy.TypeArgs) != 0 || len(copy.ValueArgs) != 0 {
				return false
			}
			copy.TypeArgs = nil
			copy.ValueArgs = nil
			if (typ.Name != "i64" && typ.Name != "int") || !reflect.ValueOf(copy).IsZero() {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ReturnHeader(fn *ast.FuncDecl, parameterSchemaValid bool, parameterLimit, byteBudget int) (*ast.ReturnStmt, bool) {
	if fn == nil || fn.Name == "" || len(fn.Name) > byteBudget || len(fn.Body) != 1 {
		return nil, false
	}
	// Every other declaration feature stays cold, including future added fields.
	header := *fn
	if !ParameterSyntax(header.Params, parameterSchemaValid, parameterLimit, byteBudget) {
		return nil, false
	}
	header.Params = nil // the parser uses a non-nil empty list for `()`
	header.Position = lexer.Pos{}
	header.Name = ""
	header.ReturnType = nil
	header.Body = nil
	if !reflect.ValueOf(header).IsZero() {
		return nil, false
	}
	switch typ := fn.ReturnType.(type) {
	case *ast.NamedType:
		copy := *typ
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if (typ.Name != "i64" && typ.Name != "int") || !reflect.ValueOf(copy).IsZero() {
			return nil, false
		}
	case *ast.BuiltinTypeExpr:
		copy := *typ
		copy.Position = lexer.Pos{}
		copy.Name = ""
		if len(copy.TypeArgs) != 0 || len(copy.ValueArgs) != 0 {
			return nil, false
		}
		copy.TypeArgs = nil
		copy.ValueArgs = nil
		if (typ.Name != "i64" && typ.Name != "int") || !reflect.ValueOf(copy).IsZero() {
			return nil, false
		}
	default:
		return nil, false
	}
	stmt, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok || stmt.Value == nil {
		return nil, false
	}
	statementHeader := *stmt
	statementHeader.Position = lexer.Pos{}
	statementHeader.Value = nil
	if !reflect.ValueOf(statementHeader).IsZero() {
		return nil, false
	}
	return stmt, true
}
