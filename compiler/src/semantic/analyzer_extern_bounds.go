package semantic

import (
	"reflect"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// `@bounds(buf, count, ...)` on a C-ABI extern (docs/127 §3.3, legacy form): the native
// prototype takes a pointer and a separate length; the Elisa-facing signature takes ONE
// bounded `view[T]` and the compiler supplies the length from `buf.count`. Pairs are
// (pointer parameter, length parameter). The extern's AST parameter list is rewritten
// BEFORE its function type is built, so every later pass (contracts, D1/D12, call
// checking) sees the bounded signature; the C parameter order survives as
// FuncType.CParamPlan for the backend.
//
// Rules, each a hard error at the annotation:
//   * arguments come in pairs of parameter names; each name is used at most once;
//   * the pointer parameter is a reference (`T&` / `mutable T&`, not optional);
//   * the length parameter is an integer;
//   * no `requires`/`ensure` names a removed length parameter (write `buf.count`);
//   * the extern is `@callconv(c)` (checked once the annotations are applied).

type externBoundsPair struct {
	ptr, length int
}

// applyExternBoundsRewrite rewrites decl in place and returns the C parameter plan and
// the removed length names, or nil when the extern has no @bounds.
func (a *Analyzer) applyExternBoundsRewrite(decl *ast.ExternFuncDecl) ([]CParamPart, []string) {
	if decl == nil {
		return nil, nil
	}
	var pairs []externBoundsPair
	used := map[int]bool{}
	for _, annotation := range decl.Annotations {
		if annotation.Name != "bounds" {
			continue
		}
		if len(annotation.Args) == 0 || len(annotation.Args)%2 != 0 {
			a.errorf(annotation.Position, "@bounds on extern function %q expects pairs of parameter names (pointer, length), got %d argument(s)", decl.Name, len(annotation.Args))
			return nil, nil
		}
		for i := 0; i+1 < len(annotation.Args); i += 2 {
			ptrIndex := externParamIndex(decl, annotation.Args[i])
			lenIndex := externParamIndex(decl, annotation.Args[i+1])
			if ptrIndex < 0 || lenIndex < 0 {
				missing := annotation.Args[i]
				if ptrIndex >= 0 {
					missing = annotation.Args[i+1]
				}
				a.errorf(annotation.Position, "@bounds on extern function %q names unknown parameter %q", decl.Name, missing)
				return nil, nil
			}
			if used[ptrIndex] || used[lenIndex] || ptrIndex == lenIndex {
				a.errorf(annotation.Position, "@bounds on extern function %q uses parameter %q more than once", decl.Name, annotation.Args[i])
				return nil, nil
			}
			ptrParam := decl.Params[ptrIndex]
			refType, isRef := ptrParam.Type.(*ast.RefType)
			if !isRef || refType == nil {
				if mut, ok := ptrParam.Type.(*ast.MutableType); ok && mut != nil {
					refType, isRef = mut.Elem.(*ast.RefType)
				}
			}
			if !isRef || refType == nil || refType.State != ast.RefStateNonNull {
				a.errorf(annotation.Position, "@bounds on extern function %q: pointer parameter %q must be a non-optional reference (`T&` or `mutable T&`)", decl.Name, ptrParam.Name)
				return nil, nil
			}
			lenParam := decl.Params[lenIndex]
			if !typeExprIsIntegerBuiltin(lenParam.Type) {
				a.errorf(annotation.Position, "@bounds on extern function %q: length parameter %q must be an integer (usize)", decl.Name, lenParam.Name)
				return nil, nil
			}
			used[ptrIndex], used[lenIndex] = true, true
			pairs = append(pairs, externBoundsPair{ptr: ptrIndex, length: lenIndex})
		}
	}
	if len(pairs) == 0 {
		return nil, nil
	}
	// A contract that names a removed length parameter has nothing to bind it to.
	mentioned := map[string]bool{}
	scanValueUsedIdents(reflect.ValueOf(decl.Requires), mentioned)
	scanValueUsedIdents(reflect.ValueOf(decl.EnsureValues), mentioned)
	scanValueUsedIdents(reflect.ValueOf(decl.Uses), mentioned)
	for _, pair := range pairs {
		if mentioned[decl.Params[pair.length].Name] {
			a.errorf(decl.Pos(), "extern function %q contract names the length parameter %q, which @bounds supplies from %s.count; write `%s.count` instead", decl.Name, decl.Params[pair.length].Name, decl.Params[pair.ptr].Name, decl.Params[pair.ptr].Name)
			return nil, nil
		}
	}
	// Rewrite: the pointer parameter becomes a view; the length parameters disappear.
	lengthOf := map[int]int{}
	for _, pair := range pairs {
		lengthOf[pair.length] = pair.ptr
	}
	ptrOf := map[int]bool{}
	for _, pair := range pairs {
		ptrOf[pair.ptr] = true
	}
	newIndex := map[int]int{}
	var rewritten []ast.ParamDecl
	var lengthNames []string
	for i, param := range decl.Params {
		if _, isLength := lengthOf[i]; isLength {
			lengthNames = append(lengthNames, param.Name)
			continue
		}
		newIndex[i] = len(rewritten)
		if ptrOf[i] {
			param.Type = boundedViewTypeExpr(param.Type)
		}
		rewritten = append(rewritten, param)
	}
	var plan []CParamPart
	for i := range decl.Params {
		if owner, isLength := lengthOf[i]; isLength {
			plan = append(plan, CParamPart{Param: newIndex[owner], Part: CParamViewLen})
			continue
		}
		if ptrOf[i] {
			plan = append(plan, CParamPart{Param: newIndex[i], Part: CParamViewPtr})
			continue
		}
		plan = append(plan, CParamPart{Param: newIndex[i], Part: CParamWhole})
	}
	decl.Params = rewritten
	return plan, lengthNames
}

// boundedViewTypeExpr turns the AST of `T&` into `view[T]` and `mutable T&` into
// `mutable view[T]`, keeping the referent's own spelling.
func boundedViewTypeExpr(t ast.TypeExpr) ast.TypeExpr {
	mutable := false
	switch n := t.(type) {
	case *ast.MutableType:
		mutable = true
		t = n.Elem
	}
	ref, ok := t.(*ast.RefType)
	if !ok || ref == nil {
		return t
	}
	view := &ast.GenericType{Position: ref.Position, Name: "view", Spelling: "view", Args: []ast.TypeExpr{ref.Elem}}
	if mutable {
		return &ast.MutableType{Position: ref.Position, Elem: view}
	}
	return view
}

func externParamIndex(decl *ast.ExternFuncDecl, name string) int {
	name = strings.TrimSpace(name)
	for i, param := range decl.Params {
		if param.Name == name {
			return i
		}
	}
	return -1
}

func typeExprIsIntegerBuiltin(t ast.TypeExpr) bool {
	switch n := t.(type) {
	case *ast.NamedType:
		switch n.Name {
		case "usize", "isize", "int", "uint", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "uintptr":
			return true
		}
	case *ast.BuiltinTypeExpr:
		switch n.Name {
		case "usize", "isize", "int", "uint", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "uintptr":
			return true
		}
	}
	return false
}

// checkExternBoundsCallConv: @bounds relies on the C-ABI view split, so the extern must
// say it is C.
func (a *Analyzer) checkExternBoundsCallConv(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || len(fnType.CParamPlan) == 0 {
		return
	}
	if !funcTypeIsCABIType(fnType) {
		a.errorf(fn.Pos(), "@bounds on extern function %q requires @callconv(c): the bounded view crosses to C as (pointer, length)", fn.Name)
	}
}

func funcTypeIsCABIType(fn *FuncType) bool {
	return fn != nil && fn.CallConv == "c"
}

var _ = lexer.Pos{}
