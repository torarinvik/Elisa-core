//go:build cgo

package backend

import (
	"go/ast"
	"go/token"
	"path"
	"strconv"
)

func codegenReadViewParameters(function *ast.FuncDecl, file *ast.File) map[string]bool {
	views := make(map[string]bool)
	semanticAliases := semanticImportAliases(file)
	add := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			if !isSemanticType(field.Type, semanticAliases, "CodegenReadView", true) {
				continue
			}
			for _, name := range field.Names {
				views[name.Name] = true
			}
		}
	}
	if function.Type != nil {
		add(function.Type.Params)
	}
	if function.Recv != nil {
		add(function.Recv)
	}
	// A local declaration with the same name shadows the typed parameter or
	// receiver. The source checker does not run Go type checking, so fail closed
	// instead of trusting that identifier after a possible shadow.
	if function.Body != nil {
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.AssignStmt:
				if declaration.Tok == token.DEFINE {
					for _, lhs := range declaration.Lhs {
						if ident, ok := lhs.(*ast.Ident); ok {
							delete(views, ident.Name)
						}
					}
				}
			case *ast.ValueSpec:
				for _, name := range declaration.Names {
					delete(views, name.Name)
				}
			case *ast.RangeStmt:
				if declaration.Tok == token.DEFINE {
					for _, expr := range []ast.Expr{declaration.Key, declaration.Value} {
						if ident, ok := expr.(*ast.Ident); ok {
							delete(views, ident.Name)
						}
					}
				}
			}
			return true
		})
	}
	return views
}

func isExplicitCodegenReadView(receiver ast.Expr, viewParams map[string]bool, function *ast.FuncDecl, structFields map[string]map[string]ast.Expr, factories map[string]bool) bool {
	for {
		switch expression := receiver.(type) {
		case *ast.ParenExpr:
			receiver = expression.X
		case *ast.StarExpr:
			receiver = expression.X
		default:
			goto unwrapped
		}
	}
unwrapped:
	if call, ok := receiver.(*ast.CallExpr); ok {
		method, ok := call.Fun.(*ast.SelectorExpr)
		return ok && factories[method.Sel.Name] && isGeneratorExpression(method.X, function, structFields)
	}
	ident, ok := receiver.(*ast.Ident)
	return ok && viewParams[ident.Name]
}

func semanticImportAliases(file *ast.File) map[string]bool {
	aliases := make(map[string]bool)
	if file == nil {
		return aliases
	}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath != "elisacore/src/semantic" {
			continue
		}
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name != "." && name != "_" {
			aliases[name] = true
		}
	}
	return aliases
}

func isSemanticType(expr ast.Expr, aliases map[string]bool, name string, allowPointer bool) bool {
	if pointer, ok := expr.(*ast.StarExpr); ok {
		if !allowPointer {
			return false
		}
		expr = pointer.X
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name]
}

func codegenReadViewFactories(files map[string]*ast.File) (map[string]bool, []string) {
	factories := map[string]bool{}
	counts := map[string]int{}
	var diagnostics []string
	reserved := map[string]bool{"semanticReadView": true, "semanticReadViewFor": true}
	aliasesByFile := make(map[string]map[string]bool, len(files))
	for filename, file := range files {
		aliasesByFile[filename] = semanticImportAliases(file)
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || !reserved[function.Name.Name] {
				continue
			}
			if filename != "llvm_codegen_read_view.go" || !isGeneratorMethodReceiver(function) || !validCodegenReadViewFactorySignature(function, aliasesByFile[filename]) {
				diagnostics = append(diagnostics, filename+": reserved read-view factory "+function.Name.Name+" is not an approved llvmGenerator bridge method")
				continue
			}
			counts[function.Name.Name]++
			factories[function.Name.Name] = true
		}
		ast.Inspect(file, func(node ast.Node) bool {
			typeSpec, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			var fields *ast.FieldList
			switch typ := typeSpec.Type.(type) {
			case *ast.StructType:
				fields = typ.Fields
			case *ast.InterfaceType:
				fields = typ.Methods
			}
			if fields != nil {
				for _, field := range fields.List {
					for _, fieldName := range field.Names {
						if reserved[fieldName.Name] {
							diagnostics = append(diagnostics, filename+": reserved read-view factory name "+fieldName.Name+" declared as a struct or interface field")
						}
					}
				}
			}
			return true
		})
	}
	for _, name := range []string{"semanticReadView", "semanticReadViewFor"} {
		if counts[name] != 1 {
			diagnostics = append(diagnostics, "expected exactly one approved llvmGenerator bridge method "+name+", found "+strconv.Itoa(counts[name]))
		}
	}
	return factories, diagnostics
}

func isGeneratorMethodReceiver(function *ast.FuncDecl) bool {
	if function == nil || function.Recv == nil || len(function.Recv.List) != 1 {
		return false
	}
	star, ok := function.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	name, ok := star.X.(*ast.Ident)
	return ok && name.Name == "llvmGenerator"
}

func validCodegenReadViewFactorySignature(function *ast.FuncDecl, aliases map[string]bool) bool {
	if function == nil || function.Type == nil || function.Type.Results == nil || len(function.Type.Results.List) != 1 || !isSemanticType(function.Type.Results.List[0].Type, aliases, "CodegenReadView", false) {
		return false
	}
	if function.Name.Name == "semanticReadView" {
		return function.Type.Params == nil || len(function.Type.Params.List) == 0
	}
	if function.Name.Name != "semanticReadViewFor" || function.Type.Params == nil || len(function.Type.Params.List) != 1 {
		return false
	}
	field := function.Type.Params.List[0]
	return len(field.Names) == 1 && isSemanticType(field.Type, aliases, "Result", true)
}

func codegenStructFields(files map[string]*ast.File) map[string]map[string]ast.Expr {
	types := make(map[string]map[string]ast.Expr)
	for _, file := range files {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, spec := range general.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok || structure.Fields == nil {
					continue
				}
				fields := make(map[string]ast.Expr)
				for _, field := range structure.Fields.List {
					for _, fieldName := range field.Names {
						fields[fieldName.Name] = field.Type
					}
				}
				types[typeSpec.Name.Name] = fields
			}
		}
	}
	return types
}

func isGeneratorExpression(expression ast.Expr, function *ast.FuncDecl, structFields map[string]map[string]ast.Expr) bool {
	if function == nil {
		return false
	}
	for {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			expression = value.X
		case *ast.StarExpr:
			expression = value.X
		default:
			goto unwrapped
		}
	}
unwrapped:
	if ident, ok := expression.(*ast.Ident); ok {
		return functionIdentifierHasType(function, ident.Name, "llvmGenerator")
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "g" {
		return false
	}
	base, ok := selector.X.(*ast.Ident)
	if !ok || !functionIdentifierHasType(function, base.Name, "functionState") {
		return false
	}
	fieldType := structFields["functionState"]["g"]
	return isNamedGoType(fieldType, "llvmGenerator")
}

func functionIdentifierHasType(function *ast.FuncDecl, identifier, typeName string) bool {
	if function == nil {
		return false
	}
	match := false
	check := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			if !isNamedGoType(field.Type, typeName) {
				continue
			}
			for _, name := range field.Names {
				if name.Name == identifier {
					match = true
				}
			}
		}
	}
	if function.Recv != nil {
		check(function.Recv)
	}
	if function.Type != nil {
		check(function.Type.Params)
	}
	if !match || function.Body == nil {
		return match
	}
	shadowed := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch declaration := node.(type) {
		case *ast.AssignStmt:
			if declaration.Tok == token.DEFINE {
				for _, lhs := range declaration.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok && ident.Name == identifier {
						shadowed = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range declaration.Names {
				if name.Name == identifier {
					shadowed = true
				}
			}
		case *ast.RangeStmt:
			if declaration.Tok == token.DEFINE {
				for _, value := range []ast.Expr{declaration.Key, declaration.Value} {
					if ident, ok := value.(*ast.Ident); ok && ident.Name == identifier {
						shadowed = true
					}
				}
			}
		}
		return !shadowed
	})
	return !shadowed
}

func isNamedGoType(expression ast.Expr, name string) bool {
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
	}
	ident, ok := expression.(*ast.Ident)
	return ok && ident.Name == name
}

func isGeneratorResultField(field *ast.SelectorExpr) bool {
	result, ok := field.X.(*ast.SelectorExpr)
	if !ok || result.Sel.Name != "result" {
		return false
	}
	generator, ok := result.X.(*ast.Ident)
	return ok && generator.Name == "g"
}
