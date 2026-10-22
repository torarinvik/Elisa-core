package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestDirectParamReturnProvenanceRecognizesSafeIdentityWrappers(t *testing.T) {
	paramType := &RefType{Elem: &BuiltinType{Name: "i64"}}
	fnType := &FuncType{Name: "identity", Params: []Type{paramType}, Return: paramType}
	analyzer := newSemanticHardeningTestAnalyzer()

	var expr ast.Expr = &ast.Ident{Name: "value"}
	wrappers := []struct {
		name string
		wrap func(ast.Expr) ast.Expr
	}{
		{name: "direct", wrap: func(expr ast.Expr) ast.Expr { return expr }},
		{name: "parentheses", wrap: func(expr ast.Expr) ast.Expr { return &ast.ParenExpr{Inner: expr} }},
		{name: "move", wrap: func(expr ast.Expr) ast.Expr { return &ast.MoveExpr{Operand: expr} }},
		{name: "can", wrap: func(expr ast.Expr) ast.Expr { return &ast.CanExpr{Expr: expr} }},
	}
	for _, tc := range wrappers {
		t.Run(tc.name, func(t *testing.T) {
			decl := &ast.FuncDecl{
				Name:   "identity",
				Params: []ast.ParamDecl{{Name: "value"}},
				Body:   []ast.Stmt{&ast.ReturnStmt{Value: tc.wrap(expr)}},
			}
			state, ok := analyzer.directParamReturnProvenance(decl, fnType)
			if !ok || !regionRefStateHasParamDep(state, 0) {
				t.Fatalf("expected wrapped identity return to depend on parameter 0, got ok=%t state=%#v", ok, state)
			}
		})
	}
}

func TestDirectParamReturnProvenanceDeclinesOtherBodies(t *testing.T) {
	paramType := &RefType{Elem: &BuiltinType{Name: "i64"}}
	fnType := &FuncType{Name: "identity", Params: []Type{paramType}, Return: paramType}
	analyzer := newSemanticHardeningTestAnalyzer()
	for _, tc := range []struct {
		name string
		edit func(*ast.FuncDecl, *FuncType)
	}{
		{name: "explicit return region", edit: func(_ *ast.FuncDecl, fnType *FuncType) { fnType.ReturnRegion = "r" }},
		{name: "different returned name", edit: func(decl *ast.FuncDecl, _ *FuncType) {
			decl.Body[0].(*ast.ReturnStmt).Value = &ast.Ident{Name: "other"}
		}},
		{name: "multiple statements", edit: func(decl *ast.FuncDecl, _ *FuncType) {
			decl.Body = append(decl.Body, &ast.ReturnStmt{Value: &ast.Ident{Name: "value"}})
		}},
		{name: "different result type", edit: func(_ *ast.FuncDecl, fnType *FuncType) { fnType.Return = &RefType{Elem: &BuiltinType{Name: "u8"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decl := &ast.FuncDecl{
				Name:   "identity",
				Params: []ast.ParamDecl{{Name: "value"}},
				Body:   []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}},
			}
			fnTypeCopy := *fnType
			tc.edit(decl, &fnTypeCopy)
			if _, ok := analyzer.directParamReturnProvenance(decl, &fnTypeCopy); ok {
				t.Fatal("fast path must decline a body or signature it cannot prove")
			}
		})
	}
}

func TestDirectParamFieldReturnProvenanceProjectsDeclaredField(t *testing.T) {
	view := &SViewType{Region: "r"}
	decl := &ast.StructDecl{Name: "Scope", Fields: []ast.FieldDecl{{Name: "view"}}}
	scope := &StructType{
		Name:   "Scope",
		Decl:   decl,
		Fields: map[string]Field{"view": {Name: "view", Type: view}},
	}
	paramType := &RefType{Elem: scope, State: RefStateNonNull}
	fnType := &FuncType{Name: "view_of", Params: []Type{paramType}, Return: view}
	fn := &ast.FuncDecl{
		Name:   "view_of",
		Params: []ast.ParamDecl{{Name: "scope"}},
		Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.CanExpr{Expr: &ast.FieldExpr{
			Object: &ast.Ident{Name: "scope"},
			Field:  "view",
		}}}},
	}

	state, ok := newSemanticHardeningTestAnalyzer().directParamFieldReturnProvenance(fn, fnType)
	if !ok || !regionRefStateHasParamDep(state, 0) {
		t.Fatalf("expected field return to depend on parameter 0, got ok=%t state=%#v", ok, state)
	}
}

func TestDirectParamFieldReturnProvenanceDeclinesUnprovedFields(t *testing.T) {
	view := &SViewType{Region: "r"}
	decl := &ast.StructDecl{Name: "Scope", Fields: []ast.FieldDecl{{Name: "view"}}}
	scope := &StructType{
		Name:   "Scope",
		Decl:   decl,
		Fields: map[string]Field{"view": {Name: "view", Type: view}},
	}
	paramType := &RefType{Elem: scope, State: RefStateNonNull}
	fnType := &FuncType{Name: "view_of", Params: []Type{paramType}, Return: view}
	for _, tc := range []struct {
		name  string
		field string
	}{
		{name: "non-parameter receiver", field: "view"},
		{name: "undeclared field", field: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := ast.Expr(&ast.Ident{Name: "scope"})
			if tc.name == "non-parameter receiver" {
				object = &ast.Ident{Name: "other"}
			}
			fn := &ast.FuncDecl{
				Name:   "view_of",
				Params: []ast.ParamDecl{{Name: "scope"}},
				Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.FieldExpr{
					Object: object,
					Field:  tc.field,
				}}},
			}
			if _, ok := newSemanticHardeningTestAnalyzer().directParamFieldReturnProvenance(fn, fnType); ok {
				t.Fatal("fast path must decline a field expression it cannot prove")
			}
		})
	}
}

func TestReusableDeclReturnProvenanceRequiresAnExactConcreteSignature(t *testing.T) {
	view := &SViewType{Region: "r"}
	fn := &ast.FuncDecl{Name: "view", Params: []ast.ParamDecl{{Name: "value"}}}
	source := &FuncType{
		Name:                  "view",
		Params:                []Type{view},
		Return:                view,
		ReturnProvenance:      regionRefStateFromParamDependency(0),
		ReturnProvenanceKnown: true,
	}
	target := &FuncType{Name: "view", Params: []Type{view}, Return: view}
	state, ok := reusableDeclReturnProvenance(fn, source, target)
	if !ok || !regionRefStateHasParamDep(state, 0) {
		t.Fatalf("expected compatible concrete summary reuse, got ok=%t state=%#v", ok, state)
	}

	changedType := *target
	changedType.Return = &RefType{Elem: &BuiltinType{Name: "i64"}}
	if _, ok := reusableDeclReturnProvenance(fn, source, &changedType); ok {
		t.Fatal("summary reuse must decline a changed return type")
	}
	changedRegion := *target
	changedRegion.ReturnRegion = "other"
	if _, ok := reusableDeclReturnProvenance(fn, source, &changedRegion); ok {
		t.Fatal("summary reuse must decline a changed return region")
	}
	changedName := *target
	changedName.Name = "Other.view"
	if _, ok := reusableDeclReturnProvenance(fn, source, &changedName); ok {
		t.Fatal("summary reuse must decline a different qualified function name")
	}
	nestedRegion := *target
	nestedRegion.Return = &SViewType{Region: "other"}
	if _, ok := reusableDeclReturnProvenance(fn, source, &nestedRegion); ok {
		t.Fatal("summary reuse must decline changed region metadata inside the return type")
	}
	regionParamFn := *fn
	regionParamFn.RegionParams = []string{"r"}
	if _, ok := reusableDeclReturnProvenance(&regionParamFn, source, target); ok {
		t.Fatal("summary reuse must decline a region-parametric declaration")
	}
	permissionParamFn := *fn
	permissionParamFn.PermissionParams = []string{"P"}
	if _, ok := reusableDeclReturnProvenance(&permissionParamFn, source, target); ok {
		t.Fatal("summary reuse must decline a permission-parametric declaration")
	}
}
